VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
IMAGE   ?= yana

# Pinned toolchain for the Android CRDT AAR. The NDK version must match
# the one the CI workflow installs; gomobile and gobind come from the
# same x/mobile release, pinned in go.mod via mobile/crdt/tools.go.
GOMOBILE_VERSION    ?= v0.0.0-20260908204917-8b95e45f8d3e
ANDROID_NDK_VERSION ?= 29.0.14206865
ANDROIDAPI          ?= 24
CRDT_AAR            ?= android/crdt/libs/yana-crdt.aar
GOPATH_BIN          := $(shell go env GOPATH)/bin

.PHONY: all web build test lint run docker android-crdt android-reader clean

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

## android-crdt: build the CRDT AAR into android/crdt/libs, where the
## Android project's crdt module picks it up. Needs a JDK (17+), the
## Android SDK, and ANDROID_NDK_HOME pointing at the pinned NDK. The
## ABIs match the app's APK splits (no 32-bit x86: emulator-only, and
## gone from current system images).
android-crdt:
	go install golang.org/x/mobile/cmd/gobind@$(GOMOBILE_VERSION)
	go install golang.org/x/mobile/cmd/gomobile@$(GOMOBILE_VERSION)
	mkdir -p $(dir $(CRDT_AAR))
	$(GOPATH_BIN)/gomobile bind -target=android/arm,android/arm64,android/amd64 \
		-androidapi=$(ANDROIDAPI) \
		-javapkg com.collinpendleton.yana.crdt -o $(CRDT_AAR) ./mobile/crdt

## android-reader: build the reader WebView's assets (the rich.ts bundle,
## its stylesheet, and the page template) with the web toolchain and copy
## them into the app's assets. Like the AAR, a build artifact: not
## committed, rebuilt by `make android-reader` and by CI.
android-reader: web
	rm -rf android/app/src/main/assets/reader
	mkdir -p android/app/src/main/assets/reader
	cp web/dist/android/reader.html web/dist/android/reader.js web/dist/android/reader.css \
		web/dist/android/fonts/* android/app/src/main/assets/reader/

clean:
	rm -f yana
	rm -rf web/dist/*
	touch web/dist/.gitkeep
