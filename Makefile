.PHONY: build test lint run up down logs simulate

LINT_VERSION ?= v2.5.0

build:
	CGO_ENABLED=0 go build -trimpath -o bin/server ./cmd/server

test:
	go test -race ./...

# Uses a local golangci-lint if installed, otherwise the official Docker image.
lint:
	go vet ./...
	@if command -v golangci-lint >/dev/null 2>&1; then golangci-lint run ./...; \
	else docker run --rm -v "$$(pwd)":/app -w /app golangci/golangci-lint:$(LINT_VERSION) golangci-lint run ./...; fi

# Runs the bot locally with variables from .env.
run:
	set -a && . ./.env && set +a && go run ./cmd/server

up:
	docker compose up -d --build

down:
	docker compose down

logs:
	docker compose logs -f app

# make simulate TEXT="/update"
simulate:
	./scripts/simulate-webhook.sh "$(or $(TEXT),/help)"
