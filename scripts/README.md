# Development Scripts

This directory contains scripts and small Go tool programs used for development, testing, and maintenance of the dd-trace-go project.

## Script Types

### Shell Scripts

- Bash scripts for common development tasks
- Can be run directly or via Makefile targets
- Automatically use development tools from `bin/` directory

### Go Programs

- Small utility programs for specific development tasks
- Should have appropriate build tags to avoid being built by `go build`:
  - `//go:build ignore` and `// +build ignore`
  - `//go:build tools` and `// +build tools`
  - `//go:build scripts` and `// +build scripts`
- Development modules should **not** be included in the `go.work` file

## Usage

### Via Makefile (Recommended)

The Makefile provides convenient targets that automatically handle tool dependencies:

```bash
make test                # Run all tests
make test-contrib        # Run contrib tests
make test-integration    # Run integration tests
make test-appsec         # Run AppSec tests
make generate            # Run code generation
make lint                # Run linting
```

### Direct Execution

Scripts can be run directly, but ensure development tools are available:

```bash
# Install tools first
make tools-install

# Run script with correct PATH
PATH="$(pwd)/bin:$PATH" ./scripts/script-name.sh

# Or run directly if script doesn't need tools
./scripts/script-name.sh
```

### Go Programs

Build and run Go programs in the scripts directory:

```bash
# Build and run a Go script
go run -tags scripts ./scripts/program-name.go

# Or if it has ignore tags
go run ./scripts/program-name.go
```

## Adding New Scripts

### Shell Scripts

1. Create the script in the `scripts/` directory
2. Make it executable: `chmod +x scripts/script-name.sh`
3. Add a Makefile target if it's commonly used
4. Use `$(BIN_PATH)` in Makefile targets to access tools

### Go Programs

1. Create the Go file in appropriate subdirectory
2. Add proper build tags to prevent inclusion in main builds
3. If it needs dependencies, create a separate `go.mod` file
4. Don't add the module to `go.work`

## Guidelines

- Scripts should be idempotent when possible
- Include error handling and clear output messages
- Document any external dependencies (Docker, etc.)
- Use development tools from `bin/` directory when available
- Keep scripts focused on single responsibilities
