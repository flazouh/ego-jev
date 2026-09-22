VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: build install test e2e e2e-live check

build:
	go build -ldflags "-X main.version=$(VERSION)" -o bin/ego-jev ./cmd/ego-jev

install:
	go install -ldflags "-X main.version=$(VERSION)" ./cmd/ego-jev

test:
	go test ./...

check:
	test -z "$$(gofmt -l .)"
	go vet ./...
	go vet -tags e2e ./...
	go test ./...

# Needs Ego Lite running.
e2e:
	go test -tags e2e -count=1 -run TestFixtureScripted ./e2e/

# Needs Ego Lite and TYPESAFE_API_KEY. Calls the real Jev and visits real sites.
e2e-live:
	EGO_JEV_LIVE=1 EGO_JEV_REAL=1 go test -tags e2e -count=1 -v ./e2e/
