# GitHub Release Notifier

A Go service that allows users to subscribe to email notifications about new releases of GitHub repositories. When a tracked repository publishes a new release, all confirmed subscribers receive an email notification.

## Live Demo

- **Frontend**: [release-notifier.michaeltal.dev](https://release-notifier.michaeltal.dev) (Cloudflare Pages)
- **API**: [api-release-notifier.michaeltal.dev](https://api-release-notifier.michaeltal.dev) (Heroku)
- **Swagger**: [`api/swagger.yaml`](api/swagger.yaml)

> The demo deployment uses Mailgun's free tier, which is limited to 100 emails/day. If you don't receive an email, this limit may have been reached.

## Architecture & Design Decisions

The application is split into two deployables that communicate over a RabbitMQ queue (see [ADR-0005](docs/adr/0005-extract-notifier-microservice.md) and [ADR-0006](docs/adr/0006-rabbitmq-message-broker.md)):

**API monolith** (`cmd/server`) — the subscription and release-detection domains:

- **API** — HTTP (Gin) and gRPC servers handling subscription management
- **Scanner** — A leader instance (Redis lock) enqueues due repositories each interval; a worker pool on any instance drains that queue and polls GitHub. Stateless and safe to run as multiple replicas

**Notifier service** (`notifier/`) — the delivery domain, a standalone module:

- **Notifier** — Worker pool that consumes the RabbitMQ `notifications` queue and sends emails (at-least-once, see below). It depends only on RabbitMQ (queue), Redis (send-dedup) and the email backend — no database — so it scales independently of the API

The scanner produces `NotificationJob`s onto the RabbitMQ `notifications` queue; the notifier service consumes them. The JSON job shape is the contract between the two.

### Why no API rate limiting?

The Swagger contract does not define `429` responses on any endpoint. Adding rate limiting would introduce response codes not present in the specification, violating the immutable contract requirement. In production, this would be handled at the infrastructure level (nginx, API gateway, or cloud load balancer).

### Why a message broker instead of a database table?

At scale (hundreds of thousands of subscribers), a PostgreSQL notification queue table accumulates millions of rows that require vacuum tuning, cleanup crons, and index maintenance. A single popular repository release generates one job per subscriber — that's potentially 100k+ rows in a single scan cycle.

A broker is purpose-built for this: O(1) enqueue/dequeue, competing consumers, and at-least-once delivery without table maintenance. We use **RabbitMQ** — durable queues, per-message ack with automatic redelivery on consumer crash, and a retry counter the broker maintains for us. The full comparison against SQS, Kafka, and the previous hand-rolled Redis queue is in [ADR-0006](docs/adr/0006-rabbitmq-message-broker.md). Notification jobs are idempotent and re-derivable, so even total broker data loss is recoverable: the next scan cycle re-detects the release and re-enqueues.

### How the scan → notify pipeline works

1. Once per `SCAN_INTERVAL`, a single **leader** instance (claimed via Redis `SET NX EX`) fetches all repositories with at least one **confirmed** subscriber (a single SQL JOIN) and pushes each onto a `repocheck` Redis queue. Non-leader instances skip the tick — no duplicate enqueues or races on the tag update
2. Worker pools across all instances drain `repocheck` (`SCAN_WORKERS`, default 5) — scan work is distributed, and any replica is interchangeable. The queue is lossy by design: a dropped repo is simply re-checked next cycle
3. Each worker calls `GetLatestRelease` via the cached GitHub client (Redis, 10min TTL). 10,000 subscribers to `golang/go` = **1 GitHub API call**, not 10,000
4. If `release.TagName != repo.LastSeenTag`, it builds a `NotificationJob` per subscriber and publishes one persistent message per subscriber to the RabbitMQ `notifications` queue
5. `last_seen_tag` in PostgreSQL is updated **only after** successful enqueue — this guarantees at-least-once delivery. If the process crashes between enqueue and tag update, the next scan re-detects the release
6. Notifier workers consume with **manual ack**: an unacked job is automatically redelivered if the worker crashes mid-send, so nothing is lost without any visibility-timeout or reaper machinery. After a two-phase dedup (**check** before sending, **mark** with TTL after — in Redis), the job is `Ack`ed. A failed send is `Nack`ed back onto the queue; the broker's `x-delivery-count` caps retries before the job is dropped (see [ADR-0006](docs/adr/0006-rabbitmq-message-broker.md))

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
- **Redis**: Pool of 15 connections, 5 kept idle (cache, leader lock, and notifier send-dedup)
- **RabbitMQ**: one long-lived connection per service; concurrency comes from channels (one consume channel per notifier worker), with automatic reconnect on drop

## Tech Stack

| Component | Technology |
|-----------|-----------|
| Language | Go 1.25 |
| HTTP Router | Gin |
| Database | PostgreSQL 16 (sqlx, no ORM) |
| Cache, lock & dedup | Redis 7 |
| Message broker | RabbitMQ 3.13 |
| Migrations | golang-migrate (embedded via `embed.FS`) |
| Email | Mailgun API |
| Metrics | Prometheus |
| Tracing | OpenTelemetry + Jaeger |
| gRPC | google.golang.org/grpc |
| Frontend | Vue.js 3 (CDN) |
| CI | GitHub Actions |
| Hosting | Heroku (API) + Cloudflare Pages (frontend) |

## Observability

The default `docker compose up` brings up the full observability stack alongside the app:

- **Kibana**: http://localhost:5601 — create a data view for `app-logs-*` to query application logs. Each entry carries `trace_id`/`span_id` when emitted inside an OTel span, so you can pivot to Jaeger (http://localhost:16686) by trace ID.
- **Elasticsearch**: http://localhost:9200
- **Prometheus**: http://localhost:9091 — scrapes both the API monolith (`app:8080`) and the notifier service (`notifier:8081`) every 15s. RED metrics are exposed for HTTP, GitHub client, notifier jobs, and scanner stages.
- **Grafana**: http://localhost:3001 (anonymous Viewer enabled, or admin/admin) — provisioned with the Prometheus datasource and the *GitHub Release Notifier — RED* dashboard (rate/errors/duration rows for HTTP, GitHub client, notifier, scanner, plus business KPIs).

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
# Requires PostgreSQL, Redis, and RabbitMQ running locally
export DATABASE_URL="postgres://user:pass@localhost:5432/release_notifier?sslmode=disable"
export REDIS_URL="redis://localhost:6379/0"
export RABBITMQ_URL="amqp://guest:guest@localhost:5672/"

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
| `REDIS_URL` | No | `redis://localhost:6379/0` | Redis connection string (cache, lock, dedup) |
| `RABBITMQ_URL` | No | `amqp://guest:guest@localhost:5672/` | RabbitMQ connection string (queue broker) |
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
| `NOTIFICATION_WORKERS` | No | `10` | Number of parallel email workers (**notifier service**) |
| `METRICS_PORT` | No | `8081` | Prometheus metrics port (**notifier service**) |
| `API_KEY` | No | — | API key for `X-API-Key` header auth (disabled if empty) |
| `DEBUG` | No | `false` | Debug logging; also activates console email backend |
| `CORS_ORIGINS` | No | `*` | Allowed CORS origins for the frontend |
| `OTEL_ENABLED` | No | `false` | Enable OpenTelemetry tracing |
| `JAEGER_ENDPOINT` | No | — | Jaeger OTLP HTTP endpoint |

## Project Structure

```
├── cmd/server/          # API monolith entrypoint and dependency wiring
├── internal/
│   ├── config/          # Environment-based configuration
│   ├── domain/          # Domain models and error types
│   ├── handler/         # HTTP handlers and middleware
│   ├── service/         # Business logic (subscribe, scan, cleanup)
│   ├── repository/      # Data access interfaces and PostgreSQL implementation
│   ├── github/          # GitHub API client with rate limit handling
│   ├── cache/           # Redis caching layer for GitHub responses
│   ├── email/           # Mailgun sender + console backend (confirmation emails)
│   ├── queue/           # RabbitMQ notification producer + Redis repo-check queue
│   ├── lock/            # Redis leader lock (scanner singleton election)
│   ├── grpc/            # gRPC server and protobuf definitions
│   └── tracing/         # OpenTelemetry setup
├── notifier/            # Delivery microservice — its own Go module (see ADR-0005)
│   ├── cmd/notifier/    # Entrypoint
│   └── internal/        # Queue consumer + dedup, email delivery, release templates
├── migrations/          # PostgreSQL schema migrations (embedded at compile time)
├── web/                 # Vue.js frontend + nginx config
├── api/                 # Swagger specification
├── Dockerfile           # Multi-stage build (API monolith)
├── docker-compose.yml   # Full stack: app + notifier + PostgreSQL + Redis + RabbitMQ + Jaeger + nginx
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

### Unit Tests

- **Subscription service** — validation, subscribe/confirm/unsubscribe flows, email failure rollback, rate limit propagation, tag seeding
- **Scanner** — leader-gated enqueue (and skip when not leader), worker repo dispatch, new release detection, no change, no releases, GitHub errors, context cancellation, enqueue errors, tag update errors, subscriber listing errors
- **Notifier** — job processing, two-phase deduplication, nack-requeue on send failure, max-retries drop, ack on terminal states, nack-for-recovery when outcome unknown, dedup check errors, mark-sent errors
- **Leader lock** — acquire, contention while held, re-acquire after expiry
- **Notification queue** (RabbitMQ) — persistent publish of each job, x-delivery-count parsing, poison-message drop, ack, nack-requeue, closed-channel and cancellation handling
- **Send dedup** (Redis) — mark with TTL, is-sent before/after, key scoping by subscription and tag
- **Cleanup** — stale subscription deletion, error handling
- **GitHub client** — rate limit header parsing, 429 retry, auth header, response decoding
- **Cached GitHub client** (Redis) — cache hits/misses, TTL behavior, caching of 200/404, no caching for 429/transient errors, key separation
- **HTTP handlers** — request validation, correct status codes for all Swagger-defined error paths
- **gRPC server** — all RPCs with error mapping to gRPC status codes

### Integration Tests

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
| Prometheus metrics | `/metrics` endpoint with RED metrics (rate, errors, duration) for HTTP, GitHub client, notifier workers, scanner stages |
| GitHub Actions CI | Lint (golangci-lint v2) → unit tests → integration tests → Docker build |
| OpenTelemetry + Jaeger | Distributed tracing (Docker only, not on Heroku) |
| Structured logging + ELK | slog JSON with trace_id/span_id correlation; Filebeat ships container logs to Elasticsearch, queryable in Kibana |
| Grafana RED dashboard | Provisioned dashboard (rate/errors/duration) for HTTP, GitHub client, notifier, scanner + business KPIs |
| Console email backend | Emails logged to stdout when Mailgun not configured |
| Subscription cleanup | Background worker removes unconfirmed subs older than 1 hour |

## Limitations

- **gRPC on Heroku** — Heroku exposes a single HTTP port per dyno. gRPC requires its own TCP port, so it is only available in Docker where both 8080 and 9090 are exposed.
- **Notifier service on Heroku** — The delivery service is a separate module/process, and the single-dyno `heroku/go` deployment only builds and runs the API monolith. So the live demo sends confirmation emails (the API sends those inline) but does not deliver release notifications — the full scan→notify pipeline runs in the `docker compose` stack, which runs both services against the shared RabbitMQ broker and Redis.
- **Mailgun free tier** — The demo deployment is limited to 100 emails/day. If you don't receive an email, this limit may have been reached.
