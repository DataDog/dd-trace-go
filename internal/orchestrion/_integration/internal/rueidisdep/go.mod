// A module outside dd-trace-go's paths that creates rueidis clients, standing
// in for a third-party library. otelc must instrument call sites in
// dependencies, not only in the module being built.
module example.com/rueidisdep

go 1.26.0

require github.com/redis/rueidis v1.0.74

require golang.org/x/sys v0.39.0 // indirect
