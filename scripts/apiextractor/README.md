# API Extractor

This command-line tool extracts the public API of a Go package by parsing its Go source files and reporting all exported functions, types, and methods.

## Usage

```bash
go run api_extractor.go [flags] <path_to_go_module>
```

Flags:

- `-gomod path/to/go.mod`: optional path to a `go.mod` file to determine the module path. If omitted, the tool searches parent directories from the given module path.

The tool writes the API report to standard output. The report contains:

- `// API Stability Report` header.
- `// Package: <modulePath>/<relativeDir>` indicating the target package.
- `// Module: <modulePath>` indicating the module root.

For each Go source file that defines exported API elements, the report lists:

```text
// File: <relative file path>

// Package Functions
func ExportedFunc(...)

// Types
type ExportedType struct { ... }

// methods (for struct types)
func (Receiver) MethodName(...)

// interface types
type ExportedInterface interface {
 MethodName(...)
}
```

## Example

Generate an API report for the `ddtrace/tracer` package and save it:

```bash
go run api_extractor.go ./ddtrace/tracer > ./ddtrace/tracer/api.txt
```
