## Re-generating the server

You can run `go generate -tags=gqlgen,integration ./...` to re-generate the server.
The `//go:generate` directives chain `scripts/add_license_headers.go` after gqlgen, so the
license headers are restored automatically in the files that were re-generated.
