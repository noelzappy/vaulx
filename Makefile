.PHONY: generate build dev

generate:
	templ generate

build: generate
	go build -o bin/server ./cmd/server

dev: generate
	air -c .air.toml || go run ./cmd/server
