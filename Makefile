GO      ?= go
BINARY  ?= notifyrelay
VERSION ?= dev

# The host toolchain may be 32-bit (windows/386), so the target is pinned.
GOOS    ?= linux
GOARCH  ?= amd64

LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: all build test vet fmt tidy run clean docker

all: vet test build

build:
	CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/notifyrelay

# Bounded on purpose: an unbounded `go test ./...` compiles every package in
# parallel and pegs every core, making the machine unusable while it runs.
GOMAXPROCS ?= 2
GOMEMLIMIT ?= 1GiB

test:
	GOMAXPROCS=$(GOMAXPROCS) GOMEMLIMIT=$(GOMEMLIMIT) $(GO) test -p 2 -parallel 2 ./...

vet:
	$(GO) vet ./...

fmt:
	$(GO) fmt ./...

tidy:
	$(GO) mod tidy

run:
	$(GO) run ./cmd/notifyrelay --config configs/notifyrelay.example.yaml

clean:
	rm -rf bin

docker:
	docker build --build-arg TARGETARCH=$(GOARCH) -t $(BINARY):$(VERSION) .
