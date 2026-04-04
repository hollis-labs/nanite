.PHONY: build install dev clean test

# Build React SPA then embed in Go binary
build: build-ui
	go build -o nanite ./cmd/nanite

# Install to ~/go/bin/ (used by MCP and Cerberus)
install: build-ui
	go install ./cmd/nanite

build-ui:
	cd ui && npm run build

# Development
dev:
	@echo "Run in two terminals:"
	@echo "  Terminal 1: air"
	@echo "  Terminal 2: cd ui && npm run dev"

# Clean build artifacts
clean:
	rm -f nanite
	rm -rf ui/dist

# Run Go tests
test:
	go test ./...

# Run with default settings
run: build
	./nanite serve --port 8090
