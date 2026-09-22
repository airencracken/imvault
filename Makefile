BINARY  := imvault
GORELEASER ?= goreleaser
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
LOGROTATEDIR ?= $(SYSCONFDIR)/logrotate.d

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo 0.1.0)
DIST    := dist
# What goes into a release tarball, in the layout `make install` expects.
DIST_FILES := cmd internal docs go.mod go.sum Makefile README.md LICENSE \
	contrib scripts Dockerfile docker-compose.yml .dockerignore

.DEFAULT_GOAL := help
.PHONY: help all build run demo test test-race test-js check check-fmt check-js check-complexity vet fmt tidy \
	install install-systemd install-openrc install-logrotate dist clean clean-demo docker \
	compose-up compose-down test-browser

.PHONY: release-check release-snapshot

release-check: ## Validate the release configuration
	"$(GORELEASER)" check

release-snapshot: release-check ## Build local binary archives and Debian packages without publishing
	"$(GORELEASER)" release --snapshot --clean --skip=publish

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
	@if command -v node >/dev/null 2>&1; then \
		node scripts/browser-check.mjs; \
	else echo "node is not installed; skipping the browser checks"; fi

test-js: ## Run the JavaScript tests (needs node)
	@if command -v node >/dev/null 2>&1; then \
		node --test internal/web/static/js/*.test.js; \
	else echo "node is not installed; skipping the JavaScript tests"; fi

vet: ## Run go vet
	go vet ./...

fmt: ## Format all Go source in place
	gofmt -w .

tidy: ## Tidy go.mod and go.sum
	go mod tidy

check-js: ## Check the JavaScript syntax (needs node)
	@if command -v node >/dev/null 2>&1; then \
		for f in internal/web/static/js/app.js internal/web/static/js/app.test.js scripts/*.mjs; do \
			node --check "$$f" || exit $$?; \
			echo "  $$f: OK"; \
		done; \
	else echo "node is not installed; skipping"; fi

check-fmt: ## Verify Go formatting without changing files
	@files=$$(gofmt -l .) || exit $$?; \
		if [ -n "$$files" ]; then printf 'Run make fmt:\n%s\n' "$$files"; exit 1; fi

check-complexity: ## Keep production Go functions at cyclomatic complexity 15 or below
	go run github.com/fzipp/gocyclo/cmd/gocyclo@v0.6.0 -over 15 -ignore '_test\.go$$' cmd internal

check: check-fmt vet test check-js test-js check-complexity ## What CI should run

# --- installing on a server -------------------------------------------------

install: build ## Install the binary under $(PREFIX)/bin
	install -d "$(DESTDIR)$(PREFIX)/bin"
	install -m755 bin/$(BINARY) "$(DESTDIR)$(PREFIX)/bin/$(BINARY)"
	@printf '\nInstalled to %s%s/bin/%s\n' "$(DESTDIR)" "$(PREFIX)" "$(BINARY)"
	@if [ -z "$(DESTDIR)" ]; then sh scripts/check-video-tools.sh; fi

install-systemd: ## Install the systemd unit and its environment file
	install -d "$(DESTDIR)$(UNITDIR)" "$(DESTDIR)$(SYSCONFDIR)/imvault"
	sed 's|/usr/local/bin/imvault|$(PREFIX)/bin/imvault|' contrib/systemd/imvault.service \
		> "$(DESTDIR)$(UNITDIR)/imvault.service"
	chmod 644 "$(DESTDIR)$(UNITDIR)/imvault.service"
	@if [ -e "$(DESTDIR)$(SYSCONFDIR)/imvault/imvault.env" ]; then \
		echo "  keeping the existing $(DESTDIR)$(SYSCONFDIR)/imvault/imvault.env"; \
	else \
		install -m640 contrib/systemd/imvault.env "$(DESTDIR)$(SYSCONFDIR)/imvault/imvault.env"; \
	fi
	@printf '\nNext:\n'
	@printf '  useradd --system --home-dir /var/lib/imvault --shell /usr/sbin/nologin imvault\n'
	@printf '  %s\n' "$${EDITOR:-vi} $(SYSCONFDIR)/imvault/imvault.env"
	@printf '  systemctl daemon-reload && systemctl enable --now imvault\n'

install-logrotate: ## Install log rotation for the OpenRC log, preserving local settings
	install -d "$(DESTDIR)$(LOGROTATEDIR)"
	@if [ -e "$(DESTDIR)$(LOGROTATEDIR)/imvault" ] || [ -L "$(DESTDIR)$(LOGROTATEDIR)/imvault" ]; then \
		echo "  keeping the existing $(DESTDIR)$(LOGROTATEDIR)/imvault"; \
	else \
		install -m644 contrib/logrotate/imvault "$(DESTDIR)$(LOGROTATEDIR)/imvault"; \
	fi
	@if [ -z "$(DESTDIR)" ]; then \
		if ! command -v logrotate >/dev/null 2>&1; then \
			printf '\nWarning: logrotate is missing. On Gentoo: emerge --ask app-admin/logrotate\n'; \
		fi; \
		printf 'Ensure logrotate runs regularly via cron or a timer. See docs/operations.md.\n'; \
	fi

install-openrc: install-logrotate ## Install the OpenRC service and log rotation (Alpine, Gentoo)
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
