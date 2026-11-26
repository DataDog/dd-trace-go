// A module outside dd-trace-go's paths that creates valkey-go clients, standing
// in for a third-party library. otelc must instrument call sites in
// dependencies, not only in the module being built.
module example.com/valkeydep

go 1.26.0

require github.com/valkey-io/valkey-go v1.0.74

require golang.org/x/sys v0.39.0 // indirect
