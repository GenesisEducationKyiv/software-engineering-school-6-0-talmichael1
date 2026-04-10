package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github-release-notifier/internal/domain"
)

const baseURL = "https://api.github.com"

// Client wraps the GitHub REST API with rate-limit awareness and retry logic.
type Client struct {
	httpClient *http.Client
	token      string

	mu             sync.Mutex
	rateLimitReset time.Time
	rateRemaining  int
}

func NewClient(token string) *Client {
	return &Client{
		httpClient:    &http.Client{Timeout: 15 * time.Second},
		token:         token,
		rateRemaining: -1,
	}
}

// RepoExists checks if a GitHub repository exists. Returns an error wrapping
// domain.ErrNotFound when the repo doesn't exist, or domain.ErrRateLimited on 429.
func (c *Client) RepoExists(ctx context.Context, owner, repo string) error {
	url := fmt.Sprintf("%s/repos/%s/%s", baseURL, owner, repo)
	_, err := c.doGet(ctx, url)
	return err
}

// GetLatestRelease fetches the most recent release for a repository.
// Returns domain.ErrNotFound if there are no releases.
func (c *Client) GetLatestRelease(ctx context.Context, owner, repo string) (*domain.Release, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/releases/latest", baseURL, owner, repo)
	body, err := c.doGet(ctx, url)
	if err != nil {
		return nil, err
	}

	var release domain.Release
	if err := json.Unmarshal(body, &release); err != nil {
		return nil, fmt.Errorf("decoding release response: %w", err)
	}
	return &release, nil
}

func (c *Client) doGet(ctx context.Context, url string) ([]byte, error) {
	c.waitForRateLimit()

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, fmt.Errorf("building request: %w", err)
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		if c.token != "" {
			req.Header.Set("Authorization", "Bearer "+c.token)
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("executing request: %w", err)
		}

		c.updateRateLimit(resp)
		body, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("reading response body: %w", readErr)
		}

		switch resp.StatusCode {
		case http.StatusOK:
			return body, nil
		case http.StatusNotFound:
			return nil, domain.ErrNotFound
		case http.StatusTooManyRequests:
			retryAfter := c.parseRetryAfter(resp)
			slog.Warn("github rate limited",
				"attempt", attempt+1,
				"retry_after", retryAfter)
			lastErr = domain.ErrRateLimited
			select {
			case <-time.After(retryAfter):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			continue
		case http.StatusForbidden:
			// GitHub sometimes returns 403 instead of 429 when rate limit is hit.
			if c.rateRemaining == 0 {
				retryAfter := time.Until(c.rateLimitReset)
				if retryAfter < 0 {
					retryAfter = 30 * time.Second
				}
				slog.Warn("github rate limit exceeded (403)",
					"attempt", attempt+1,
					"retry_after", retryAfter)
				lastErr = domain.ErrRateLimited
				select {
				case <-time.After(retryAfter):
				case <-ctx.Done():
					return nil, ctx.Err()
				}
				continue
			}
			return nil, fmt.Errorf("%w: HTTP %d", domain.ErrExternalAPI, resp.StatusCode)
		default:
			return nil, fmt.Errorf("%w: HTTP %d", domain.ErrExternalAPI, resp.StatusCode)
		}
	}
	return nil, fmt.Errorf("max retries exceeded: %w", lastErr)
}

// waitForRateLimit blocks if we know the rate limit is exhausted.
func (c *Client) waitForRateLimit() {
	c.mu.Lock()
	remaining := c.rateRemaining
	reset := c.rateLimitReset
	c.mu.Unlock()

	if remaining >= 0 && remaining < 5 && time.Now().Before(reset) {
		wait := time.Until(reset)
		slog.Info("preemptive rate limit wait", "wait", wait, "remaining", remaining)
		time.Sleep(wait)
	}
}

// updateRateLimit reads GitHub rate limit headers and stores them.
func (c *Client) updateRateLimit(resp *http.Response) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if v := resp.Header.Get("X-RateLimit-Remaining"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.rateRemaining = n
		}
	}
	if v := resp.Header.Get("X-RateLimit-Reset"); v != "" {
		if ts, err := strconv.ParseInt(v, 10, 64); err == nil {
			c.rateLimitReset = time.Unix(ts, 0)
		}
	}
}

func (c *Client) parseRetryAfter(resp *http.Response) time.Duration {
	if v := resp.Header.Get("Retry-After"); v != "" {
		if secs, err := strconv.Atoi(v); err == nil {
			return time.Duration(secs) * time.Second
		}
	}
	// Fallback: use rate limit reset header or a default backoff.
	if !c.rateLimitReset.IsZero() {
		if d := time.Until(c.rateLimitReset); d > 0 {
			return d
		}
	}
	return 60 * time.Second
}
