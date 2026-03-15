BINARY     := azstral
CMD        := ./cmd/azstral
INSTALL_DIR := $(HOME)/.local/bin

.PHONY: build install uninstall test clean mcp-add mcp-remove

## Build the azstral binary.
build:
	go build -o $(BINARY) $(CMD)

## Build and install to ~/.local/bin, then register as a user-level MCP server.
install: build
	install -m 0755 $(BINARY) $(INSTALL_DIR)/$(BINARY)
	@if claude mcp get $(BINARY) >/dev/null 2>&1; then \
		echo "MCP server '$(BINARY)' already registered"; \
	else \
		claude mcp add -s user $(BINARY) $(INSTALL_DIR)/$(BINARY); \
		echo "Registered MCP server '$(BINARY)'"; \
	fi

## Remove the MCP registration and delete the binary from ~/.local/bin.
uninstall:
	-claude mcp remove $(BINARY) -s user 2>/dev/null
	-rm -f $(INSTALL_DIR)/$(BINARY)

## Run all tests.
test:
	go test ./...

## Re-register the MCP server (useful after changing the binary path).
mcp-add:
	claude mcp add -s user $(BINARY) $(INSTALL_DIR)/$(BINARY)

## Remove the MCP server registration without deleting the binary.
mcp-remove:
	claude mcp remove $(BINARY) -s user

## Remove the local build artifact.
clean:
	rm -f $(BINARY)
