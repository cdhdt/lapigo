.PHONY: all build build-all test test-race fmt fmt-check vet lint check clean

GO      ?= go
PKGS    := ./...
BIN     := bin/lapigo

all: check

# build-all compiles every package, which is what CI does and what `check`
# needs. `build` links the CLI binary and only works once cmd/lapigo exists
# (build order step 9), so it is deliberately not part of `check`.
build-all:
	$(GO) build $(PKGS)

build:
	$(GO) build -o $(BIN) ./cmd/lapigo

# -count=1 everywhere: Go caches test results by package content, so a cached
# "ok" after a change elsewhere is indistinguishable from a real pass.
test:
	$(GO) test -count=1 $(PKGS)

test-race:
	$(GO) test -count=1 -race $(PKGS)

# Regenerate golden files, then review the diff before committing.
golden:
	$(GO) test $(PKGS) -run 'Golden' -update

fmt:
	$(GO) run golang.org/x/tools/cmd/goimports@latest -w .

fmt-check:
	@out=$$(gofmt -l .); \
	if [ -n "$$out" ]; then echo "not gofmt-clean:"; echo "$$out"; exit 1; fi

vet:
	$(GO) vet $(PKGS)

lint:
	$(GO) run honnef.co/go/tools/cmd/staticcheck@2025.1.1 $(PKGS)

# check must run everything CI runs, in the same order. It did not include
# lint, so a staticcheck failure reached develop with a green local check and a
# red pipeline nobody looked at. If CI gains a step, it gains one here too.
check: fmt-check vet lint test-race build-all

clean:
	rm -rf bin
