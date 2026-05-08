VERSION := $(shell git describe --tags --dirty --always)

.PHONY: build
build:
	go build -ldflags "-X github.com/b1rd33/tgctl-go/internal/commands.Version=$(VERSION)" -o tg ./cmd/tg
