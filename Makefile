VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
IMAGE   ?= yana

.PHONY: all web build test lint run docker clean

all: build

## web: build the browser client into web/dist
web:
	cd web && npm ci && npm run build

## build: build the yana binary (runs `web` first so the client is embedded)
build: web
	CGO_ENABLED=0 go build -trimpath -ldflags "-X main.version=$(VERSION)" -o yana ./cmd/yana

## test: run the Go suite and the web typecheck once
test:
	go test ./...
	cd web && npm run typecheck

## lint: gofmt and go vet
lint:
	test -z "$$(gofmt -l .)" || (gofmt -l . && exit 1)
	go vet ./...

## run: build and serve ./notes on :8080
run: build
	YANA_NOTES_ROOT=./notes ./yana

## docker: build the container image
docker:
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION) -t $(IMAGE):latest .

clean:
	rm -f yana
	rm -rf web/dist/*
	touch web/dist/.gitkeep
