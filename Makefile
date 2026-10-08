BIN := $(HOME)/bin/pitpilot

.PHONY: build test install

build:
	go build -o bin/pitpilot ./cmd/pitpilot

test:
	go test -race ./...
	go vet ./...

install:
	go build -o $(BIN) ./cmd/pitpilot
