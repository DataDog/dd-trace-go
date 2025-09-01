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

[embedmd]:# (../tmp/make-help.txt)
```txt
Usage: make [target]

Targets:
  help                 Show this help message
  all                  Run complete build pipeline (tools, generate, lint, test)
  tools-install        Install development tools
  clean                Clean build artifacts
  clean-all            Clean everything including tools and temporary files
  generate             Run code generation
  lint                 Run linting checks
  lint-fix             Fix linting issues automatically
  format               Format code
  test                 Run all tests (core, integration, contrib)
  test-appsec          Run tests with AppSec enabled
  test-contrib         Run contrib package tests
  test-integration     Run integration tests
  test-deadlock        Run tests with deadlock detection
  test-debug-deadlock  Run tests with debug and deadlock detection
  fix-modules          Fix module dependencies and consistency
  docs                 Update embedded documentation in README files
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

#### Test Script Options

The test script provides many options for different testing scenarios:

[embedmd]:# (../tmp/test-help.txt)
```txt
test.sh - Run the tests for dd-trace-go
	this script requires gotestsum, goimports, docker and docker-compose.
	-a | --appsec		- Test with appsec enabled
	-i | --integration	- Run integration tests. This requires docker and docker-compose. Resource usage is significant when combined with --contrib
	-c | --contrib		- Run contrib tests
	--all			- Synonym for -l -a -i -c
	-s | --sleep		- The amount of seconds to wait for docker containers to be ready - default: 30 seconds
	-t | --tools		- Install gotestsum and goimports
	-h | --help		- Print this help message
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
3. Add a Makefile target if it's commonly used (follow the pattern of existing targets)
4. Use `$(BIN_PATH)` in Makefile targets to access development tools from `bin/`

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
