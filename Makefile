GO ?= go
BIN := bin/dshfit
PKG := github.com/atop0914/dshfit
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "")
DATE    ?= $(shell git log -1 --format=%cI 2>/dev/null || echo "")
LDFLAGS := -s -w \
	-X $(PKG)/internal/version.Version=$(VERSION) \
	-X $(PKG)/internal/version.Commit=$(COMMIT) \
	-X $(PKG)/internal/version.Date=$(DATE)

.PHONY: all
all: fmt-check vet test build-check

.PHONY: fmt
fmt:
	gofmt -s -w $(shell find . -name '*.go' -not -path './bin/*')

.PHONY: fmt-check
fmt-check:
	@out=$$(gofmt -s -l $$(find . -name '*.go' -not -path './bin/*')); \
	if [ -n "$$out" ]; then echo "UNFORMATTED:"; echo "$$out"; exit 1; fi

.PHONY: vet
vet:
	$(GO) vet ./...

.PHONY: test
test:
	$(GO) test -count=1 ./...

# The gate that matters: a skipped test is a test that proves nothing, and this
# project's whole value rests on assertions that can fail.
.PHONY: test-noskip
test-noskip:
	@out=$$($(GO) test -count=1 -v ./... 2>&1); \
	echo "$$out" | grep -E '^(ok|FAIL|---)' | tail -5; \
	n=$$(echo "$$out" | grep -c -- '--- SKIP' || true); \
	echo "SKIP=$$n"; \
	if [ "$$n" -ne 0 ]; then echo "skipped tests are not allowed"; exit 1; fi; \
	if echo "$$out" | grep -q -- '--- FAIL'; then exit 1; fi

.PHONY: race
race:
	$(GO) test -count=1 -race -timeout 300s ./...

# Fuzzing is a separate target on purpose, and it is not in `ci`.
#
# A fuzz run finds inputs by chance: a green run says nothing about what it did
# not try, and a job that goes red at a random minute on an input nobody kept is
# a job people turn off. What CI runs instead is the corpus — the seeds inside
# each target, plus anything the fuzzer wrote to testdata/fuzz/, every entry of
# which is an ordinary regression test. When a run does fail there, commit the
# file it wrote: that is the evidence, and it keeps working after the fuzzer
# stops.
FUZZTIME ?= 10s
.PHONY: fuzz
fuzz:
	@for t in FuzzParseRange FuzzExplain; do \
		echo "--- semver/$$t $(FUZZTIME)"; \
		$(GO) test -run=^$$ -fuzz=^$$t$$ -fuzztime=$(FUZZTIME) ./internal/semver/ || exit 1; \
	done
	@for t in FuzzRegistryParse FuzzCacheRecord; do \
		echo "--- registry/$$t $(FUZZTIME)"; \
		$(GO) test -run=^$$ -fuzz=^$$t$$ -fuzztime=$(FUZZTIME) ./internal/registry/ || exit 1; \
	done

.PHONY: build
build:
	@mkdir -p bin
	$(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN) ./cmd/dshfit

# Zero dependencies is a feature here: the tool must be installable and runnable
# inside a CI job that has no network and no module cache.
.PHONY: deps-check
deps-check:
	@n=$$($(GO) list -m all | wc -l); echo "modules=$$n"; \
	if [ "$$n" -ne 1 ]; then echo "runtime dependencies are not allowed"; exit 1; fi

.PHONY: build-check
build-check:
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; \
		echo "build $$os/$$arch"; \
		GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 $(GO) build -trimpath -o /dev/null ./cmd/dshfit || exit 1; \
	done
	@echo "vet darwin (catches build-tag rot on the other unix)"
	@GOOS=darwin GOARCH=arm64 $(GO) vet ./...

.PHONY: lint
lint:
	golangci-lint run ./...

.PHONY: ci
ci: fmt-check vet test-noskip race deps-check build-check lint

.PHONY: clean
clean:
	rm -rf bin
