# Local entry points for the diagnostics product. CI runs `lint`, `test` and
# `acceptance`, one step per target; nothing here pushes or publishes.
SHELL := /bin/sh
GO ?= go

.PHONY: all lint fmt-check vet test acceptance pack clean

all: lint test

# fmt-check fails on any file gofmt would rewrite, so a stale format cannot hide
# behind a passing test run.
fmt-check:
	@unformatted=$$(gofmt -l $$(git ls-files '*.go')); \
	if [ -n "$$unformatted" ]; then \
		echo "gofmt required for:"; echo "$$unformatted"; exit 1; \
	fi

vet:
	$(GO) vet ./...

lint: fmt-check vet

# test runs the unit and acceptance suites; the cases read a fake API server, so
# no cluster is needed.
test:
	$(GO) test ./...

acceptance:
	$(GO) test ./tests/... -count=1 -v

# pack writes the extension package and its digest. The product declares no UI
# asset, so a missing assets/ directory yields the two-entry package.
pack:
	$(GO) run ./cmd/kos-diagnostics-pack -out dist/kos-diagnostics.tar

clean:
	$(GO) clean -testcache
