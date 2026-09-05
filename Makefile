BINARY_NAME=chest
BINARY_WIN=$(BINARY_NAME).exe

.PHONY: all help build build-win run test clean tidy install

# Default target
all: build

help:
	@echo CHEST Makefile Targets:
	@echo   make build        - Compile chest binary for current OS
	@echo   make build-win    - Compile chest.exe binary for Windows
	@echo   make run          - Run chest CLI directly using go run
	@echo   make test         - Run test suite across all packages
	@echo   make tidy         - Run go mod tidy to clean up dependencies
	@echo   make clean        - Remove compiled binaries
	@echo   make install      - Install binary into GOPATH/bin
	@echo   make help         - Show this help message

--help: help

build:
	go build -o $(BINARY_NAME) ./cmd/chest

build-win:
	go build -o $(BINARY_WIN) ./cmd/chest

run:
	go run ./cmd/chest

test:
	go test -v ./...

tidy:
	go mod tidy

clean:
	@if exist $(BINARY_WIN) del /f /q $(BINARY_WIN)
	@if exist $(BINARY_NAME) del /f /q $(BINARY_NAME)

install:
	go install ./cmd/chest
