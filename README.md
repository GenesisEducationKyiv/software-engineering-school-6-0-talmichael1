# GitHub Release Notifier

A Go service that allows users to subscribe to email notifications about new releases of GitHub repositories. When a tracked repository publishes a new release, all confirmed subscribers receive an email notification.

## Live Demo

- **Frontend**: [release-notifier.michaeltal.dev](https://release-notifier.michaeltal.dev) (Cloudflare Pages)
- **API**: [api-release-notifier.michaeltal.dev](https://api-release-notifier.michaeltal.dev) (Heroku)
- **Swagger**: [`api/swagger.yaml`](api/swagger.yaml)

> The demo deployment uses Mailgun's free tier, which is limited to 100 emails/day. If you don't receive an email, this limit may have been reached.

## Architecture & Design Decisions

The application is a monolith with three logical components running within a single process:

- **API** — HTTP (Gin) and gRPC servers handling subscription management
- **Scanner** — Background worker pool that periodically polls GitHub for new releases in parallel
- **Notifier** — Worker pool that consumes a Redis queue and sends emails

### Why no API rate limiting?

The Swagger contract does not define `429` responses on any endpoint. Adding rate limiting would introduce response codes not present in the specification, violating the immutable contract requirement. In production, this would be handled at the infrastructure level (nginx, API gateway, or cloud load balancer).

### Why Redis queue instead of a database table?

At scale (hundreds of thousands of subscribers), a PostgreSQL notification queue table accumulates millions of rows that require vacuum tuning, cleanup crons, and index maintenance. A single popular repository release generates one job per subscriber — that's potentially 100k+ rows in a single scan cycle.

Redis lists provide O(1) push/pop with automatic memory reclaim. LPUSH/BRPOP is purpose-built for job queues. The tradeoff is durability — Redis is not as durable as PostgreSQL. For notification jobs (which are idempotent and can be re-derived from the scan cycle), this is an acceptable tradeoff. If Redis loses data, the next scan cycle re-detects the release and re-enqueues.

### How the scan → notify pipeline works

1. The scanner fetches all repositories that have at least one **confirmed** subscriber (a single SQL JOIN — repos with zero confirmed subs are never checked)
2. Repositories are distributed across a configurable worker pool (`SCAN_WORKERS`, default 5) via a channel — each repo is checked by exactly one worker, and all external clients (`go-redis`, `sqlx.DB`) are concurrency-safe
3. Each worker calls `GetLatestRelease` via the cached GitHub client (Redis, 10min TTL). 10,000 subscribers to `golang/go` = **1 GitHub API call**, not 10,000
4. If `release.TagName != repo.LastSeenTag`, it builds a `NotificationJob` per subscriber and enqueues them all to Redis in a single pipeline (`LPUSH`)
5. `last_seen_tag` in PostgreSQL is updated **only after** successful enqueue — this guarantees at-least-once delivery. If the process crashes between enqueue and tag update, the next scan re-detects the release
6. Notifier workers (`BRPOP`) use a two-phase deduplication strategy: **check** (`EXISTS`) before sending to skip known duplicates, then **mark** (`SET` with TTL) after successful delivery. This prevents both lost notifications (mark-before-send) and duplicate emails (mark-after-send covers 99.9% of cases)

### Why seed `last_seen_tag` on subscribe?

Without this, a user subscribing to a repo with an existing release (e.g., v0.1.5 from February) would immediately receive a notification for that old release. On subscribe, we fetch the current latest release and store its tag, so the scanner only notifies on genuinely new releases published *after* the subscription was created.

### Why confirmation + unsubscribe tokens?

Each subscription generates two `crypto/rand` tokens (32 bytes, hex-encoded):
- **confirm_token** — sent in the confirmation email, activates the subscription
- **unsubscribe_token** — included in every notification email, allows one-click unsubscribe

Unconfirmed subscriptions never trigger notifications. A background cleanup worker removes unconfirmed subscriptions older than 1 hour (runs every 30 minutes) to prevent database bloat from abandoned signups.

### Why rollback on email failure?

If the confirmation email fails to send (Mailgun down, invalid domain, etc.), the subscription is immediately deleted. Without this, the user would get a 500 error and a retry would return 409 (conflict) — a dead end. The rollback allows clean retries.

### GitHub rate limit strategy

The client reads `X-RateLimit-Remaining` and `X-RateLimit-Reset` headers from every GitHub response. When remaining requests drop below 5, it preemptively waits until the reset window. On `429` responses, it respects the `Retry-After` header and retries up to 3 times. GitHub sometimes returns `403` instead of `429` when rate-limited — the client detects this by checking if `X-RateLimit-Remaining` is 0.

Without a `GITHUB_TOKEN`, the limit is 60 requests/hour. With a token (any GitHub PAT, no special scopes needed), it's 5,000/hour.

### Connection pooling

- **PostgreSQL**: 18 max connections, 8 idle (Heroku essential-0 allows 20 — we leave headroom for migrations and admin tools)
- **Redis**: Pool of 15 connections, 5 kept idle (enough for 10 notification workers + scanner + API handlers)

## Tech Stack

| Component | Technology |
|-----------|-----------|
| Language | Go 1.25 |
| HTTP Router | Gin |
| Database | PostgreSQL 16 (sqlx, no ORM) |
| Cache & Queue | Redis 7 |
| Migrations | golang-migrate (embedded via `embed.FS`) |
| Email | Mailgun API |
| Metrics | Prometheus |
| Tracing | OpenTelemetry + Jaeger |
| gRPC | google.golang.org/grpc |
| Frontend | Vue.js 3 (CDN) |
| CI | GitHub Actions |
| Hosting | Heroku (API) + Cloudflare Pages (frontend) |

## Quick Start

### Prerequisites

- Docker and Docker Compose

### Running

```bash
# Copy and edit environment variables
cp .env.example .env

# Start all services
docker compose up --build
```

The application will be available at:
- **Frontend**: http://localhost:3000 (nginx, proxies `/api` to the backend)
- **REST API**: http://localhost:8080
- **gRPC**: localhost:9090
- **Prometheus metrics**: http://localhost:8080/metrics
- **Jaeger UI**: http://localhost:16686

When Mailgun credentials are not configured, emails are logged to stdout (console email backend, similar to Django's `console.EmailBackend`).

### Running Locally (without Docker)

```bash
# Requires PostgreSQL and Redis running locally
export DATABASE_URL="postgres://user:pass@localhost:5432/release_notifier?sslmode=disable"
export REDIS_URL="redis://localhost:6379/0"

go run ./cmd/server
```

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/subscribe` | Subscribe an email to release notifications |
| `GET` | `/api/confirm/{token}` | Confirm email subscription |
| `GET` | `/api/unsubscribe/{token}` | Unsubscribe from notifications |
| `GET` | `/api/subscriptions?email={email}` | List all subscriptions for an email |
| `GET` | `/health` | Health check |
| `GET` | `/metrics` | Prometheus metrics |

### Subscribe

```bash
curl -X POST http://localhost:8080/api/subscribe \
  -H "Content-Type: application/json" \
  -d '{"email": "user@example.com", "repo": "golang/go"}'
```

**Responses:**
- `200` — Subscription created, confirmation email sent
- `400` — Invalid email or repository format
- `404` — Repository not found on GitHub
- `409` — Email already subscribed to this repository

Full API documentation available in [`api/swagger.yaml`](api/swagger.yaml).

## Configuration

All configuration is done via environment variables:

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `DATABASE_URL` | Yes | — | PostgreSQL connection string |
| `REDIS_URL` | No | `redis://localhost:6379/0` | Redis connection string |
| `MAILGUN_DOMAIN` | No | — | Mailgun sending domain (console backend if empty) |
| `MAILGUN_API_KEY` | No | — | Mailgun API key (console backend if empty) |
| `MAILGUN_FROM` | No | `noreply@releases.app` | Sender email address |
| `MAILGUN_API_BASE` | No | — | Mailgun API base URL (`https://api.eu.mailgun.net/v3` for EU) |
| `GITHUB_TOKEN` | No | — | GitHub PAT (raises rate limit from 60 to 5000 req/hr) |
| `PORT` | No | `8080` | HTTP server port |
| `GRPC_PORT` | No | `9090` | gRPC server port |
| `BASE_URL` | No | `http://localhost:8080` | Base URL for email links |
| `SCAN_INTERVAL` | No | `5m` | How often to check for new releases |
| `SCAN_WORKERS` | No | `5` | Number of parallel scanner workers |
| `NOTIFICATION_WORKERS` | No | `10` | Number of parallel email workers |
| `API_KEY` | No | — | API key for `X-API-Key` header auth (disabled if empty) |
| `DEBUG` | No | `false` | Debug logging; also activates console email backend |
| `CORS_ORIGINS` | No | `*` | Allowed CORS origins for the frontend |
| `OTEL_ENABLED` | No | `false` | Enable OpenTelemetry tracing |
| `JAEGER_ENDPOINT` | No | — | Jaeger OTLP HTTP endpoint |

## Project Structure

```
├── cmd/server/          # Entrypoint and dependency wiring
├── internal/
│   ├── config/          # Environment-based configuration
│   ├── domain/          # Domain models and error types
│   ├── handler/         # HTTP handlers and middleware
│   ├── service/         # Business logic (subscribe, scan, notify, cleanup)
│   ├── repository/      # Data access interfaces and PostgreSQL implementation
│   ├── github/          # GitHub API client with rate limit handling
│   ├── cache/           # Redis caching layer for GitHub responses
│   ├── email/           # Mailgun sender + console backend
│   ├── queue/           # Redis-backed notification queue with deduplication
│   ├── grpc/            # gRPC server and protobuf definitions
│   └── tracing/         # OpenTelemetry setup
├── migrations/          # PostgreSQL schema migrations (embedded at compile time)
├── web/                 # Vue.js frontend + nginx config
├── api/                 # Swagger specification
├── Dockerfile           # Multi-stage build
├── docker-compose.yml   # Full stack: app + PostgreSQL + Redis + Jaeger + nginx
└── .github/workflows/   # CI pipeline (lint, test, build)
```

## Testing

```bash
# Unit tests
make test

# Integration tests (requires PostgreSQL)
DATABASE_URL="postgres://user:pass@localhost:5432/test_db?sslmode=disable" \
  make test-integration
```

### Unit Tests (55 tests)

- **Subscription service** — validation, subscribe/confirm/unsubscribe flows, email failure rollback, rate limit propagation, tag seeding
- **Scanner** — new release detection, no change, no releases, GitHub errors, context cancellation, enqueue errors, tag update errors, subscriber listing errors
- **Notifier** — job processing, two-phase deduplication, retry logic, max retries, dedup check errors, mark-sent errors
- **Cleanup** — stale subscription deletion, error handling
- **GitHub client** — rate limit header parsing, 429 retry, auth header, response decoding
- **HTTP handlers** — request validation, correct status codes for all Swagger-defined error paths
- **gRPC server** — all RPCs with error mapping to gRPC status codes

### Integration Tests (21 tests)

End-to-end tests hitting a real PostgreSQL database:

- **API flow** — subscribe → confirm → unsubscribe lifecycle, duplicates, invalid input, empty responses
- **Repository store** — GetOrCreate idempotency, tag updates, active subscription filtering
- **Subscription store** — CRUD, duplicate constraint, token lookups, confirmed filtering, stale cleanup

## Extras

| Extra | Implementation |
|-------|---------------|
| Deploy + HTML page | Heroku (API) + Cloudflare Pages (Vue.js frontend) |
| gRPC interface | Port 9090, same service layer as REST — no duplicated logic |
| Redis caching | GitHub API responses cached with 10-minute TTL |
| API key auth | Optional `X-API-Key` header (disabled when `API_KEY` is empty) |
| Prometheus metrics | `/metrics` endpoint with request counts, latencies, notification counters |
| GitHub Actions CI | Lint (golangci-lint v2) → unit tests → integration tests → Docker build |
| OpenTelemetry + Jaeger | Distributed tracing (Docker only, not on Heroku) |
| Console email backend | Emails logged to stdout when Mailgun not configured |
| Subscription cleanup | Background worker removes unconfirmed subs older than 1 hour |

## Limitations

- **gRPC on Heroku** — Heroku exposes a single HTTP port per dyno. gRPC requires its own TCP port, so it is only available in Docker where both 8080 and 9090 are exposed.
- **Mailgun free tier** — The demo deployment is limited to 100 emails/day. If you don't receive an email, this limit may have been reached.
