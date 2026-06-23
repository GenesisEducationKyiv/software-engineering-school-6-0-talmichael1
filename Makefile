.PHONY: build run test test-integration lint docker-up docker-down proto buf-lint buf-generate bench-confirmation bench-confirmation-ext clean

build:
	go build -o bin/server ./cmd/server

run: build
	./bin/server

test:
	go test -race -count=1 ./...

test-integration:
	go test -race -count=1 -tags=integration ./internal/repository/...

lint:
	golangci-lint run ./...

proto:
	protoc --go_out=. --go_opt=paths=source_relative \
		--go-grpc_out=. --go-grpc_opt=paths=source_relative \
		internal/grpc/proto/subscription.proto

# Confirmation contract (ADR-0008) is generated with buf; config in buf.yaml / buf.gen.yaml.
buf-lint:
	buf lint

buf-generate:
	buf generate

# Fair in-process comparison (same Go harness drives both transports).
bench-confirmation:
	cd notifier && go run ./cmd/confirmbench -load

# Optional external cross-check with ghz (gRPC) + autocannon (REST). Note these
# are different tools, so their absolute numbers are not directly comparable.
bench-confirmation-ext:
	./scripts/bench/confirmation.sh

docker-up:
	docker compose up --build -d

docker-down:
	docker compose down

clean:
	rm -rf bin/
