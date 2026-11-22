BINARY     := pulsar
MODULE     := github.com/agomez/pulsar
VERSION    := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
GIT_COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_DATE := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS    := -X $(MODULE)/internal/version.Version=$(VERSION) \
              -X $(MODULE)/internal/version.GitCommit=$(GIT_COMMIT) \
              -X $(MODULE)/internal/version.BuildDate=$(BUILD_DATE)

# Remote test host for the *-remote / up / down / logs targets. Set it in a
# gitignored local.mk (see local.mk.example) or on the command line:
#   make up-local TEST_HOST=user@host
-include local.mk
TEST_HOST  ?=
TEST_DIR   ?= /home/ubuntu/pulsar

.PHONY: build pulsarctl pulsarctl-linux deploy-pulsarctl proto test test-remote sync-remote lint \
        dev-up dev-down \
        up up-local down logs ps \
        docker docker-web docker-local docker-remote migrate-up migrate-down tidy \
        web-install web-build web-dev web-lint build-full \
        promote-admin roadmap-pdf helm-lint check-test-host

## Build the single binary
build:
	go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/pulsar

## Install web UI dependencies
web-install:
	cd web && npm ci

## Build the React UI into web/dist/
web-build:
	cd web && npm run build

## Start the Vite dev server
web-dev:
	cd web && npm run dev

## Type-check the web UI
web-lint:
	cd web && npm run typecheck

## Build Go binary with embedded UI
build-full: web-build
	go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/pulsar

## Build the CLI client (native platform, for local use)
pulsarctl:
	go build -ldflags "$(LDFLAGS)" -o bin/pulsarctl ./cmd/pulsarctl

## Build the CLI client for linux/amd64 (for deployment to controller)
pulsarctl-linux:
	GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o bin/pulsarctl-linux ./cmd/pulsarctl

## Build linux/amd64 pulsarctl and deploy it to the controller host
deploy-pulsarctl: check-test-host pulsarctl-linux
	scp bin/pulsarctl-linux $(TEST_HOST):$(TEST_DIR)/bin/pulsarctl

## Generate protobuf Go code (requires protoc + protoc-gen-go + protoc-gen-go-grpc)
proto:
	protoc \
		--go_out=gen/proto/agent   --go_opt=paths=source_relative \
		--go-grpc_out=gen/proto/agent --go-grpc_opt=paths=source_relative \
		-I internal/proto internal/proto/agent.proto
	protoc \
		--go_out=gen/proto/compute --go_opt=paths=source_relative \
		-I internal/proto internal/proto/compute.proto
	protoc \
		--go_out=gen/proto/network --go_opt=paths=source_relative \
		-I internal/proto internal/proto/network.proto
	protoc \
		--go_out=gen/proto/storage --go_opt=paths=source_relative \
		-I internal/proto internal/proto/storage.proto

## Run all tests locally
test:
	go test ./... -race -count=1

## Sync source to test host and run tests there
sync-remote: check-test-host
	rsync -av --exclude='.git' --exclude='bin/' --exclude='backup-files/' --exclude='Users/' \
	  ./ $(TEST_HOST):$(TEST_DIR)/

test-remote: sync-remote
	ssh $(TEST_HOST) "cd $(TEST_DIR) && PATH=\$$PATH:/usr/local/go/bin go test ./... -race -count=1"

## Run linter (requires golangci-lint)
lint:
	golangci-lint run ./...

## Start local dev dependencies only (etcd + postgres, no Pulsar services)
dev-up:
	docker compose -f deploy/docker/docker-compose.dev.yml up -d

## Stop local dev dependencies
dev-down:
	docker compose -f deploy/docker/docker-compose.dev.yml down

## Build the Pulsar Docker image
docker:
	docker build -f deploy/docker/Dockerfile \
	  --platform linux/amd64 \
	  --build-arg VERSION=$(VERSION) \
	  --build-arg GIT_COMMIT=$(GIT_COMMIT) \
	  --build-arg BUILD_DATE=$(BUILD_DATE) \
	  -t pulsar:dev -t pulsar:$(VERSION) .

## Build the web frontend Docker image locally
docker-web:
	docker build \
	  --platform linux/amd64 \
	  -f deploy/docker/Dockerfile.web \
	  -t pulsar-web:dev .

## Build image on the test host (avoids cross-arch transfer)
docker-remote: sync-remote
	ssh $(TEST_HOST) "cd $(TEST_DIR) && docker build \
	  -f deploy/docker/Dockerfile \
	  --build-arg VERSION=$(VERSION) \
	  --build-arg GIT_COMMIT=$(GIT_COMMIT) \
	  -t pulsar:dev ."

## Build both images locally and ship to the test host via docker save | load
docker-local: check-test-host
	docker build \
	  --platform linux/amd64 \
	  -f deploy/docker/Dockerfile \
	  --build-arg VERSION=$(VERSION) \
	  --build-arg GIT_COMMIT=$(GIT_COMMIT) \
	  --build-arg BUILD_DATE=$(BUILD_DATE) \
	  -t pulsar:dev .
	docker build \
	  --platform linux/amd64 \
	  -f deploy/docker/Dockerfile.web \
	  -t pulsar-web:dev .
	docker save pulsar:dev pulsar-web:dev | ssh $(TEST_HOST) "docker load"

## Start full stack using locally-built images (build here, run there)
up-local: sync-remote docker-local
	ssh $(TEST_HOST) "cd $(TEST_DIR)/deploy/docker && docker compose up -d"

## Start full stack on test host
up: sync-remote docker-remote
	ssh $(TEST_HOST) "cd $(TEST_DIR)/deploy/docker && docker compose up -d"

## Sync deploy config and restart a single agent (no rebuild)
## Usage: make restart-agent SERVICE=agent-storage
restart-agent: sync-remote
	ssh $(TEST_HOST) "cd $(TEST_DIR)/deploy/docker && docker compose up -d --no-build $(SERVICE)"

## Stop full stack on test host
down: check-test-host
	ssh $(TEST_HOST) "cd $(TEST_DIR)/deploy/docker && docker compose down"

## Tail logs from all services on test host
logs: check-test-host
	ssh $(TEST_HOST) "cd $(TEST_DIR)/deploy/docker && docker compose logs -f"

## Show service status on test host
ps: check-test-host
	ssh $(TEST_HOST) "cd $(TEST_DIR)/deploy/docker && docker compose ps"

## Run database migrations (up)
migrate-up:
	migrate -path internal/store/postgres/migrations -database "$(PG_DSN)" up

## Run database migrations (down)
migrate-down:
	migrate -path internal/store/postgres/migrations -database "$(PG_DSN)" down

## Tidy go modules
tidy:
	go mod tidy

## Promote a user to admin by email.
## Usage: make promote-admin EMAIL=you@example.com
## Works against the running stack on TEST_HOST.
promote-admin: check-test-host
	@test -n "$(EMAIL)" || (echo "Usage: make promote-admin EMAIL=<user@example.com>" && exit 1)
	ssh $(TEST_HOST) "docker exec -i $$(cd $(TEST_DIR)/deploy/docker && docker compose ps -q postgres) \
	  psql -U pulsar pulsar -c \
	  \"UPDATE users SET role = 'admin' WHERE email = '$(EMAIL)'; SELECT id, email, role FROM users WHERE email = '$(EMAIL)';\""

## Export docs/pulsar-roadmap.html to PDF with headless Chrome.
## Override: make roadmap-pdf CHROME=/path/to/chrome ROADMAP_PDF=out.pdf
CHROME      ?= /Applications/Google Chrome.app/Contents/MacOS/Google Chrome
ROADMAP_PDF ?= docs/pulsar-roadmap.pdf
roadmap-pdf:
	"$(CHROME)" --headless --disable-gpu --no-pdf-header-footer \
	  --print-to-pdf="$(ROADMAP_PDF)" "file://$(CURDIR)/docs/pulsar-roadmap.html"

## Lint and render the Helm chart (same checks as CI, minus kubeconform)
HELM_CHART := deploy/kubernetes/helm/pulsar
helm-lint:
	helm lint --strict $(HELM_CHART) --set jwt.secret=local-lint-only-0123456789abcdef \
	  --set infraCompute.enabled=true,infraNetwork.enabled=true,infraStorage.enabled=true
	helm template pulsar $(HELM_CHART) --set jwt.secret=local-lint-only-0123456789abcdef > /dev/null
	@! helm template pulsar $(HELM_CHART) > /dev/null 2>&1 || (echo "chart rendered without jwt.secret" && exit 1)
	@echo "helm chart OK"

## Fail fast when a remote target runs without TEST_HOST configured.
check-test-host:
	@test -n "$(TEST_HOST)" || (echo "TEST_HOST is not set. Copy local.mk.example to local.mk and set it, or pass TEST_HOST=user@host." && exit 1)
