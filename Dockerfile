FROM golang:1.25-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
# The API imports the Notifier's confirmation proto contract via a local replace
# (ADR-0008); its go.mod must be present for module resolution before download.
COPY notifier/go.mod notifier/go.sum ./notifier/
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /server ./cmd/server

FROM alpine:3.19

RUN apk add --no-cache ca-certificates tzdata

COPY --from=builder /server /server
COPY --from=builder /app/migrations /migrations

EXPOSE 8080 9090

CMD ["/server"]
