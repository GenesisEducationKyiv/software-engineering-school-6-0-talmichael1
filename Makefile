.PHONY: build run test lint docker-up docker-down proto clean

build:
	go build -o bin/server ./cmd/server

run: build
	./bin/server

test:
	go test -race -count=1 ./...

lint:
	golangci-lint run ./...

proto:
	protoc --go_out=. --go_opt=paths=source_relative \
		--go-grpc_out=. --go-grpc_opt=paths=source_relative \
		internal/grpc/proto/subscription.proto

docker-up:
	docker compose up --build -d

docker-down:
	docker compose down

clean:
	rm -rf bin/
