# Every toolchain runs inside a pinned container, so local runs and CI match.
# Only Docker (with Compose) and make are needed on the host.

GO_IMAGE   := golang:1.27.4-bookworm
NODE_IMAGE := node:22.23.5-bookworm-slim
NETWORK    := kacheem_default
UIDGID     := $(shell id -u):$(shell id -g)

# Inside the compose network the services use their container ports.
TEST_DATABASE_URL := postgres://app:dev-only-password@db:5432/tickets?sslmode=disable
TEST_REDIS_URL    := redis://:dev-only-redis@redis:6379/0

DOCKER_RUN := docker run --rm -u $(UIDGID) --network $(NETWORK) \
	-v $(CURDIR):/repo -e HOME=/repo/.cache/home

GO := $(DOCKER_RUN) -w /repo/backend \
	-e GOPATH=/repo/.cache/go -e GOCACHE=/repo/.cache/go-build \
	-e GOTOOLCHAIN=local -e CGO_ENABLED=1 \
	-e TEST_DATABASE_URL='$(TEST_DATABASE_URL)' -e TEST_REDIS_URL='$(TEST_REDIS_URL)' \
	-e DATABASE_URL='$(TEST_DATABASE_URL)' \
	$(GO_IMAGE)

NODE := $(DOCKER_RUN) -w /repo/web -e npm_config_cache=/repo/.cache/npm \
	-e NEXT_TELEMETRY_DISABLED=1 $(NODE_IMAGE)

.PHONY: help up down reset cache \
	go-fmt go-fmt-fix go-vet go-test go-cover go-build go-vulncheck migrate \
	web-install web-lint web-test web-build \
	verify verify-go verify-web

help: ## List targets
	@grep -E '^[a-zA-Z_-]+:.*## ' $(MAKEFILE_LIST) | awk -F ':.*## ' '{printf "  %-14s %s\n", $$1, $$2}'

cache:
	@mkdir -p .cache/home .cache/go .cache/go-build .cache/npm

up: ## Start local PostgreSQL and Redis
	docker compose up -d --wait db redis

down: ## Stop local services (keeps data)
	docker compose down

reset: ## Stop local services and delete their data
	docker compose down -v

# ---------- Go ----------

go-fmt: cache ## Fail if any Go file is not gofmt-formatted
	@out="$$($(GO) gofmt -l .)"; if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

go-fmt-fix: cache ## Rewrite Go files with gofmt
	$(GO) gofmt -w .

go-vet: cache
	$(GO) go vet ./...

go-test: cache up ## Go tests with the race detector against real PostgreSQL and Redis
	$(GO) go test -race -count=1 -covermode=atomic -coverpkg=./... -coverprofile=coverage.out ./...

go-cover: go-test ## Enforce 100% Go statement coverage
	$(GO) sh ../scripts/check-go-coverage.sh coverage.out

go-build: cache
	$(GO) sh -c 'mkdir -p bin && go build -trimpath -o bin/ ./cmd/...'

go-vulncheck: cache
	$(GO) go tool govulncheck ./...

migrate: cache up ## Apply migrations to the local database
	$(GO) go run ./cmd/migrate up

# ---------- Web ----------

web-install: cache
	$(NODE) npm ci --no-audit --no-fund

web-lint: web-install
	$(NODE) npm run lint --workspaces

web-test: web-install ## Frontend tests with the 100% coverage gate
	$(NODE) npm run test:coverage --workspaces

web-build: web-install ## Static exports into web/*/out
	$(NODE) npm run build --workspaces

# ---------- Gate ----------

verify-go: go-fmt go-vet go-cover go-vulncheck go-build
verify-web: web-lint web-test web-build

verify: verify-go verify-web ## Everything that must pass before a manual push
	@echo "verify: OK"
