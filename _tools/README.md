# Development Tools

This directory contains development tools used for building, testing, and maintaining the dd-trace-go project. The tools are defined as blank imports in `tools.go` and their dependencies are managed in `go.mod`.

## Installation

To install all development tools into the `bin/` directory, run from the project root:

```bash
make tools-install
```

This will:

1. Create the `bin/` directory if it doesn't exist
2. Download all tool dependencies
3. Install all tools to `bin/` directory

## Usage

Once installed, tools can be used in two ways:

### Via Makefile Targets

The Makefile provides convenient targets that automatically use the correct tool versions:

```bash
# e.g.
make lint           # Run linter
make test           # Run all tests
make generate       # Generate code
```

### Direct Tool Usage

Tools can be run directly from the `bin/` directory or by using the `BIN_PATH` variable:

```bash
# Run tools directly
./bin/tool-name [args]

# Or use with correct PATH
PATH="$(pwd)/bin:$PATH" tool-name [args]
```

## Adding New Tools

To add a new development tool:

1. Add a blank import to `tools.go`:

   ```go
   _ "example.com/new-tool/cmd/tool"
   ```

2. Run `go mod tidy` to update dependencies:

   ```bash
   cd _tools && go mod tidy
   ```

3. Install tools:

   ```bash
   make tools-install
   ```

## Module Independence

The `_tools` directory is a separate Go module that is intentionally **not** included in the workspace (`go.work`). This ensures:

- Tool dependencies don't interfere with the main project
- Tool versions are managed independently
- Clean separation between runtime and development dependencies

## Troubleshooting

If tools fail to install:

1. Ensure you're running from the project root
2. Check that `_tools/go.mod` is properly maintained
3. Verify tools are correctly imported in `tools.go`
4. Run `cd _tools && go mod tidy` to sync dependencies
