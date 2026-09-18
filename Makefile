BINARY  := imvault
PKG     := ./cmd/imvault
GOFLAGS ?=

# Local run knobs. Override on the command line, e.g. `make run PORT=9000`.
PORT     ?= 8080
DATA_DIR ?= ./data
ADDR     ?= :$(PORT)

.DEFAULT_GOAL := help
.PHONY: help all build run demo test test-race vet fmt check tidy clean clean-demo docker compose-up compose-down

help: ## Show the available targets
	@printf '\nimvault\n\n'
	@grep -hE '^[a-zA-Z0-9_-]+:.*?## ' $(MAKEFILE_LIST) \
		| sort \
		| awk 'BEGIN {FS = ":.*## "} {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'
	@printf '\nNew here? `make demo` boots a throwaway instance seeded with sample images.\n\n'

all: check build ## Format, vet, test and build

build: ## Build the server into bin/imvault
	go build $(GOFLAGS) -o bin/$(BINARY) $(PKG)

run: ## Run on :8080 using ./data (override PORT=, DATA_DIR=)
	IMVAULT_ADDR=$(ADDR) IMVAULT_DATA_DIR=$(DATA_DIR) go run $(GOFLAGS) $(PKG)

demo: ## Boot a throwaway instance seeded with sample media
	@./scripts/demo.sh

test: ## Run the test suite
	go test $(GOFLAGS) ./...

test-race: ## Run the test suite under the race detector
	go test $(GOFLAGS) -race ./...

vet: ## Run go vet
	go vet ./...

fmt: ## Format all Go source in place
	gofmt -w .

tidy: ## Tidy go.mod and go.sum
	go mod tidy

check: fmt vet test ## What CI should run

clean: ## Remove build output
	rm -rf bin

clean-demo: ## Remove the demo data directory
	rm -rf ./demo-data

docker: ## Build the container image
	docker build -t $(BINARY) .

compose-up: ## Start the compose stack in the background
	docker compose up --build -d

compose-down: ## Stop the compose stack
	docker compose down
