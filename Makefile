-include .env.mk

VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE := $(shell date -u +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || echo unknown)
LDFLAGS := -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.buildDate=$(BUILD_DATE)

.PHONY: build build-solo build-team frontend frontend-solo frontend-team marketing \
	test deploy-staging deploy-prod revert-staging revert-prod visual-test dev dev-fresh

# Editions are a build-time split (see internal/api/routes_core.go). The
# default is `managed` — everything — because that is what deploy-staging and
# deploy-prod ship; a forgotten tag there would hand customers a binary with
# no signup, teams or admin. The self-host builds are opt-in.
build: frontend
	go build -ldflags "$(LDFLAGS)" -o dist/rekam ./cmd/rekam

# Self-host, one person: no teams, no user management, no control plane.
# Each edition needs its own frontend build too — the SPA is embedded in the
# binary, so a solo server built against the managed bundle would serve a UI
# full of buttons its own routes 404.
build-solo: frontend-solo
	go build -tags solo -ldflags "$(LDFLAGS)" -o dist/rekam-solo ./cmd/rekam

# Self-host with collaborators: shared corpora and user management, but no
# self-service signup and no plan tiers — it cannot be run as a service.
build-team: frontend-team
	go build -tags team -ldflags "$(LDFLAGS)" -o dist/rekam-team ./cmd/rekam

# What CI runs. All three editions, because each compiles a different route
# surface and therefore a different test set — `go test ./...` alone only
# covers managed, and the solo and team suites went red unnoticed once
# already. Assumes internal/api/web is populated; run `make frontend` first
# on a clean checkout.
test:
	@for tags in "" team solo; do \
		echo "==> edition $${tags:-managed}"; \
		go vet -tags "$$tags" ./... || exit 1; \
		go test -tags "$$tags" ./... -race || exit 1; \
	done

# Screenshot-driven Playwright suite (browser runs in Docker; see tests/visual).
visual-test:
	./tests/visual/run.sh

# Build + seed a local instance and leave it running for you to click through
# in a real browser (landing page, login, admin console, ...). Reuses whatever
# data tests/visual/.devdata already has; `make dev-fresh` wipes and reseeds
# first. Ctrl-C stops it. See tests/visual/dev.sh for the printed login.
dev:
	./tests/visual/dev.sh

dev-fresh:
	./tests/visual/dev.sh --fresh

frontend:
	cd frontend && npm run build

# The marketing site: landing page + legal documents, built as static pages
# for the site root rather than embedded in the binary. Output is
# dist/marketing/, which is what gets published to the edge — see
# frontend/vite.marketing.config.js for why it is a separate build.
marketing:
	cd frontend && npm run build:marketing

frontend-solo:
	cd frontend && REKAM_EDITION=solo npm run build

frontend-team:
	cd frontend && REKAM_EDITION=team npm run build

# Staging: deploys whatever is currently checked out (any commit, dirty
# working tree included) — it's meant to run ahead of prod for testing.
deploy-staging:
	cd frontend && npm run build
	GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/rekam-linux ./cmd/rekam
	git push
	scp dist/rekam-linux $(STAGING_REMOTE):$(STAGING_REMOTE_DIR)/rekam.new
	ssh $(STAGING_REMOTE) 'cd $(STAGING_REMOTE_DIR) \
		&& cp -f rekam rekam.prev 2>/dev/null; \
		mv rekam.new rekam \
		&& chmod +x rekam \
		&& systemctl --user restart $(STAGING_SERVICE) \
		&& systemctl --user status $(STAGING_SERVICE) --no-pager -l'

# Prod: only ever deploys a tagged release, from a clean tree — cut a
# release first (git tag vX.Y.Z && git push origin vX.Y.Z), validate it on
# staging, then promote it here.
deploy-prod:
	@git diff --quiet && git diff --cached --quiet || \
		{ echo "ERROR: working tree has uncommitted changes - commit and tag a release before deploying to prod" >&2; exit 1; }
	@git describe --tags --exact-match HEAD >/dev/null 2>&1 || \
		{ echo "ERROR: HEAD is not exactly on a git tag - prod deploys must be from a tagged release (git tag vX.Y.Z && git push origin vX.Y.Z)" >&2; exit 1; }
	cd frontend && npm run build
	GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/rekam-linux ./cmd/rekam
	scp dist/rekam-linux $(PROD_REMOTE):$(PROD_REMOTE_DIR)/rekam.new
	ssh $(PROD_REMOTE) 'cd $(PROD_REMOTE_DIR) \
		&& cp -f rekam rekam.prev 2>/dev/null; \
		mv rekam.new rekam \
		&& chmod +x rekam \
		&& systemctl --user restart $(PROD_SERVICE) \
		&& systemctl --user status $(PROD_SERVICE) --no-pager -l'

revert-staging:
	ssh $(STAGING_REMOTE) 'cd $(STAGING_REMOTE_DIR) \
		&& cp -f rekam.prev rekam \
		&& systemctl --user restart $(STAGING_SERVICE) \
		&& systemctl --user status $(STAGING_SERVICE) --no-pager -l'

revert-prod:
	ssh $(PROD_REMOTE) 'cd $(PROD_REMOTE_DIR) \
		&& cp -f rekam.prev rekam \
		&& systemctl --user restart $(PROD_SERVICE) \
		&& systemctl --user status $(PROD_SERVICE) --no-pager -l'
