.PHONY: all build test test-race fmt fmt-check vet lint check clean

GO      ?= go
PKGS    := ./...
BIN     := bin/lapigo

all: check build

build:
	$(GO) build -o $(BIN) ./cmd/lapigo

test:
	$(GO) test $(PKGS)

test-race:
	$(GO) test -race $(PKGS)

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

check: fmt-check vet test-race

clean:
	rm -rf bin
