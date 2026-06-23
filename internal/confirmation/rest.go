// Package confirmation sends subscription confirmation emails by calling the
// Notifier participant of the subscribe saga (ADR-0007). RESTClient is the HTTP
// transport; a gRPC sibling behind the same Send signature follows in HW10.
package confirmation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

type RESTClient struct {
	endpoint string
	http     *http.Client
}

func NewRESTClient(baseURL string, timeout time.Duration) *RESTClient {
	return &RESTClient{
		endpoint: strings.TrimRight(baseURL, "/") + "/internal/confirmations",
		http:     &http.Client{Timeout: timeout},
	}
}

type payload struct {
	Email      string `json:"email"`
	Repo       string `json:"repo"`
	ConfirmURL string `json:"confirm_url"`
}

func (c *RESTClient) Send(ctx context.Context, to, repo, confirmURL string) error {
	body, err := json.Marshal(payload{Email: to, Repo: repo, ConfirmURL: confirmURL})
	if err != nil {
		return fmt.Errorf("encoding confirmation request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("building confirmation request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(req.Header))

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("calling notifier: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("notifier confirmation returned %d", resp.StatusCode)
	}
	return nil
}
