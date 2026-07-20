.PHONY: build test lint clean install

VERSION ?= dev
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT)
BIN     := bin/dot

build:
	go build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/dot/

test:
	go test ./... -race -count=1

lint:
	golangci-lint run ./...

clean:
	rm -rf bin/

install: build
	@mkdir -p $(HOME)/.local/bin
	install -m 0755 $(BIN) $(HOME)/.local/bin/.dot.new
	mv $(HOME)/.local/bin/.dot.new $(HOME)/.local/bin/dot
	ln -sf $(HOME)/.local/bin/dot $(HOME)/.local/bin/dotfiles
