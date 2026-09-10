## Re-generating the server

You can run `go generate -tags=gqlgen,integration ./...` to re-generate the server. After doing so, you will
need to manually fix the licensing headers in all files that were re-generated and lost it.
