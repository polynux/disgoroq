.PHONY: test test-unit test-integration test-all run build clean help

# Set PKG_CONFIG_PATH for libdave (Discord E2EE voice)
# Set CGO_CFLAGS to suppress Opus warnings and enable optimization
PKG_CONFIG_PATH := $(HOME)/.local/lib/pkgconfig
CGO_CFLAGS := -Wno-stringop-overread -O2

test: test-all
	@echo "Running all tests..."

test-unit:
	@echo "Running unit tests..."
	@go test -v ./database/ ./horoscope/ ./ai/ ./commands/

test-integration:
	@echo "Running integration tests..."
	@go test -v -tags=integration ./...

test-all:
	@echo "Running all tests..."
	@go test -v ./...

run:
	@echo "Running bot..."
	@PKG_CONFIG_PATH="$(PKG_CONFIG_PATH)" CGO_CFLAGS="$(CGO_CFLAGS)" go run main.go -local

build:
	@echo "Building bot..."
	@PKG_CONFIG_PATH="$(PKG_CONFIG_PATH)" CGO_CFLAGS="$(CGO_CFLAGS)" go build -o disgoroq main.go

clean:
	@echo "Cleaning build artifacts..."
	@rm -f disgoroq
	@rm -rf tmp/

help:
	@echo "Available targets:"
	@echo "  test       - Run all tests (alias for test-all)"
	@echo "  test-unit  - Run unit tests only"
	@echo "  test-integration - Run integration tests only"
	@echo "  test-all   - Run all tests"
	@echo "  run        - Run the bot with local database"
	@echo "  build      - Build the bot binary"
	@echo "  clean      - Remove build artifacts"
	@echo "  help       - Show this help message"
