VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

# --- Debian packaging / apt repository (see packaging/) ---------------------
# Owner, repo and URL of the public apt repository, in one place. The release
# workflow (.github/workflows/release.yml) has matching env values.
APT_OWNER ?= artw1
APT_REPO  ?= aslcp
APT_URL   ?= https://$(APT_OWNER).github.io/$(APT_REPO)

# nfpm needs Go >= 1.25; with GOTOOLCHAIN=auto (the default) go fetches it.
NFPM_VERSION ?= v2.46.3
NFPM = go run github.com/goreleaser/nfpm/v2/cmd/nfpm@$(NFPM_VERSION)

# Package version: tag vX.Y.Z -> X.Y.Z. Untagged (test builds, e.g. try/* branches):
# X.Y.Z+dev.N.g<sha> after the last release tag (N commits later), which sorts above
# X.Y.Z and below the next release, so the next real release installs over it.
# Without any release tag: 0.0.0~git<shortsha>.
GIT_TAG     := $(shell git describe --tags --exact-match --match 'v[0-9]*.[0-9]*.[0-9]*' --exclude '*-*' 2>/dev/null)
GIT_DESC    := $(shell git describe --tags --long --match 'v[0-9]*.[0-9]*.[0-9]*' --exclude '*-*' 2>/dev/null)
DEV_VERSION := $(if $(GIT_DESC),$(shell echo '$(GIT_DESC)' | sed -E 's/^v([0-9]+\.[0-9]+\.[0-9]+)-([0-9]+)-(g[0-9a-f]+)$$/\1+dev.\2.\3/'),0.0.0~git$(shell git rev-parse --short HEAD 2>/dev/null || echo unknown))
DEB_VERSION ?= $(if $(GIT_TAG),$(patsubst v%,%,$(GIT_TAG)),$(DEV_VERSION))
DEB_LDFLAGS  = -s -w -X main.version=$(DEB_VERSION)
ARCHES      := arm64 armhf amd64

.PHONY: print-version web build build-arm64 build-armhf test vet demo clean deb deb-pkg debs

web:
	cd web && npm ci && npm run build
	find internal/webui/dist -mindepth 1 ! -name .gitkeep ! -name .gitignore -exec rm -rf {} +
	cp -R web/dist/. internal/webui/dist/

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/aslcp ./cmd/aslcp

build-arm64:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/aslcp-linux-arm64 ./cmd/aslcp

build-armhf:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/aslcp-linux-armhf ./cmd/aslcp

test:
	go test -race ./...

vet:
	go vet ./...

# Simulated node, no Asterisk needed. Login: demo / demo. Vite dev server
# (http://localhost:5173) is an allowed origin.
demo:
	go run ./cmd/aslcp --demo --dev --config /dev/null

# make deb ARCH=arm64|armhf|amd64  ->  dist/aslcp_<version>_<arch>.deb
deb: web
	$(MAKE) deb-pkg ARCH=$(ARCH)

# Same as deb without rebuilding the web UI (used by `debs` and CI).
deb-pkg:
	@case "$(ARCH)" in \
	  arm64) goarch=arm64; goarm= ;; \
	  armhf) goarch=arm;   goarm=7 ;; \
	  amd64) goarch=amd64; goarm= ;; \
	  *) echo "usage: make deb ARCH=arm64|armhf|amd64"; exit 2 ;; \
	esac; \
	d=dist/build/$(ARCH); mkdir -p $$d; \
	for c in aslcp aslcp-helper; do \
	  CGO_ENABLED=0 GOOS=linux GOARCH=$$goarch GOARM=$$goarm go build -trimpath -ldflags "$(DEB_LDFLAGS)" -o $$d/$$c ./cmd/$$c || exit 1; \
	done; \
	sed "s|@BUILD_DIR@|$$d|g" packaging/nfpm.yaml > $$d/nfpm.yaml; \
	VERSION="$(DEB_VERSION)" ARCH="$(ARCH)" $(NFPM) package -f $$d/nfpm.yaml -p deb -t dist/

# All three architectures -> dist/*.deb
debs: web
	@for a in $(ARCHES); do $(MAKE) deb-pkg ARCH=$$a || exit 1; done

clean:
	rm -rf bin dist

print-version:
	@echo $(DEB_VERSION)
