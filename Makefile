.PHONY: build test lint clean install

VERSION ?= dev
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT)
BIN     := bin/dotfiles

build:
	go build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/dotfiles/

test:
	go test ./... -race -count=1

lint:
	golangci-lint run ./...

clean:
	rm -rf bin/

install: build
	cp $(BIN) $(HOME)/.local/bin/dotfiles
