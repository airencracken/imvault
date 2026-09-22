BINARY  := imvault
PKG     := ./cmd/imvault
GOFLAGS ?=

# Local run knobs. Override on the command line, e.g. `make run PORT=9000`.
PORT     ?= 8080
DATA_DIR ?= ./data
ADDR     ?= :$(PORT)

# Install knobs. `make install DESTDIR=/tmp/stage PREFIX=/usr` stages a package.
PREFIX     ?= /usr/local
DESTDIR    ?=
SYSCONFDIR ?= /etc
UNITDIR    ?= $(SYSCONFDIR)/systemd/system

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo 0.1.0)
DIST    := dist
# What goes into a release tarball, in the layout `make install` expects.
DIST_FILES := cmd internal docs go.mod go.sum Makefile README.md LICENSE \
	contrib scripts Dockerfile docker-compose.yml .dockerignore

.DEFAULT_GOAL := help
.PHONY: help all build run demo test test-race test-js check check-js vet fmt tidy \
	install install-systemd install-openrc dist clean clean-demo docker \
	compose-up compose-down test-browser

help: ## Show the available targets
	@printf '\nimvault\n\n'
	@grep -hE '^[a-zA-Z0-9_-]+:.*?## ' $(MAKEFILE_LIST) \
		| sort \
		| awk 'BEGIN {FS = ":.*## "} {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'
	@printf '\nNew here? `make demo` boots a throwaway instance seeded with sample images.\n\n'

all: check build ## Format, vet, test and build

build: ## Build the server into bin/imvault
	go build $(GOFLAGS) -o bin/$(BINARY) $(PKG)

run: ## Run on :8080 using ./data (override PORT=, DATA_DIR=)
	IMVAULT_ADDR=$(ADDR) IMVAULT_DATA_DIR=$(DATA_DIR) go run $(GOFLAGS) $(PKG)

demo: ## Boot a throwaway instance seeded with a representative one
	@./scripts/demo.sh

test: ## Run the Go test suite
	go test $(GOFLAGS) ./...

test-race: ## Run the Go test suite under the race detector
	go test $(GOFLAGS) -race ./...

test-browser: ## Drive a real browser against a throwaway instance (needs node + chromium)
	@command -v node >/dev/null 2>&1 \
		&& node scripts/browser-check.mjs \
		|| echo "node is not installed; skipping the browser checks"

test-js: ## Run the JavaScript tests (needs node)
	@command -v node >/dev/null 2>&1 \
		&& node --test internal/web/static/js/ \
		|| echo "node is not installed; skipping the JavaScript tests"

vet: ## Run go vet
	go vet ./...

fmt: ## Format all Go source in place
	gofmt -w .

tidy: ## Tidy go.mod and go.sum
	go mod tidy

check-js: ## Check the JavaScript syntax (needs node)
	@command -v node >/dev/null 2>&1 \
		&& for f in internal/web/static/js/app.js internal/web/static/js/app.test.js; do \
			node --check "$$f" && echo "  $$f: OK"; \
		done \
		|| echo "node is not installed; skipping"

check: fmt vet test check-js ## What CI should run

# --- installing on a server -------------------------------------------------

install: build ## Install the binary under $(PREFIX)/bin
	install -d "$(DESTDIR)$(PREFIX)/bin"
	install -m755 bin/$(BINARY) "$(DESTDIR)$(PREFIX)/bin/$(BINARY)"
	@printf '\nInstalled to %s%s/bin/%s\n' "$(DESTDIR)" "$(PREFIX)" "$(BINARY)"

install-systemd: ## Install the systemd unit and its environment file
	install -d "$(DESTDIR)$(UNITDIR)" "$(DESTDIR)$(SYSCONFDIR)/imvault"
	install -m644 contrib/systemd/imvault.service "$(DESTDIR)$(UNITDIR)/imvault.service"
	@if [ -e "$(DESTDIR)$(SYSCONFDIR)/imvault/imvault.env" ]; then \
		echo "  keeping the existing $(DESTDIR)$(SYSCONFDIR)/imvault/imvault.env"; \
	else \
		install -m640 contrib/systemd/imvault.env "$(DESTDIR)$(SYSCONFDIR)/imvault/imvault.env"; \
	fi
	@printf '\nNext:\n'
	@printf '  useradd --system --home-dir /var/lib/imvault --shell /usr/sbin/nologin imvault\n'
	@printf '  %s\n' "$${EDITOR:-vi} $(SYSCONFDIR)/imvault/imvault.env"
	@printf '  systemctl daemon-reload && systemctl enable --now imvault\n'

install-openrc: ## Install the OpenRC service (Alpine, Gentoo)
	install -Dm755 contrib/openrc/imvault "$(DESTDIR)/etc/init.d/imvault"
	@if [ -e "$(DESTDIR)/etc/conf.d/imvault" ]; then \
		echo "  keeping the existing $(DESTDIR)/etc/conf.d/imvault"; \
	else \
		install -Dm644 contrib/openrc/imvault.confd "$(DESTDIR)/etc/conf.d/imvault"; \
	fi
	@printf '\nNext:\n'
	@printf '  adduser -S -D -H -h /var/lib/imvault -s /sbin/nologin imvault   # Alpine\n'
	@printf '  useradd --system --home-dir /var/lib/imvault -s /sbin/nologin imvault  # Gentoo\n'
	@printf '  rc-update add imvault default && rc-service imvault start\n'
# The init script looks in /usr/bin, which is where a package puts it. An
# install that lands anywhere else needs to say so, or the service starts and
# immediately stops with nothing but "command not found" in the log.
	@if [ "$(PREFIX)" != "/usr" ]; then \
		printf '\n  The service looks for the binary at /usr/bin/%s, but this\n' "$(BINARY)"; \
		printf '  installed it under %s/bin. Either reinstall with PREFIX=/usr,\n' "$(PREFIX)"; \
		printf '  or set IMVAULT_BIN=%s/bin/%s in /etc/conf.d/imvault.\n' "$(PREFIX)" "$(BINARY)"; \
	fi

dist: ## Build a release tarball in dist/
	@rm -rf "$(DIST)/$(BINARY)-$(VERSION)"
	@mkdir -p "$(DIST)/$(BINARY)-$(VERSION)"
	@tar -c $(DIST_FILES) | tar -x -C "$(DIST)/$(BINARY)-$(VERSION)"
	@tar -czf "$(DIST)/$(BINARY)-$(VERSION).tar.gz" -C "$(DIST)" "$(BINARY)-$(VERSION)"
	@rm -rf "$(DIST)/$(BINARY)-$(VERSION)"
	@printf 'Wrote %s/%s-%s.tar.gz\n' "$(DIST)" "$(BINARY)" "$(VERSION)"

# --- containers and housekeeping --------------------------------------------

docker: ## Build the container image
	docker build -t $(BINARY) .

compose-up: ## Start the compose stack in the background
	docker compose up --build -d

compose-down: ## Stop the compose stack
	docker compose down

clean: ## Remove build output
	rm -rf bin dist

clean-demo: ## Remove the demo data directory
	rm -rf ./demo-data
