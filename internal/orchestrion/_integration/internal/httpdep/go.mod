// A module outside dd-trace-go's paths that calls the net/http client
// shorthands, standing in for a third-party library. otelc must rewrite call
// sites in dependencies, not only in the module being built.
module example.com/httpdep

go 1.26.0
