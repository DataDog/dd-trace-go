// A module outside dd-trace-go's paths that logs with zap, standing in for a
// third-party library. otelc must instrument call sites in dependencies, not
// only in the module being built.
module example.com/zapdep

go 1.26.0

require go.uber.org/zap v1.28.0

require go.uber.org/multierr v1.10.0 // indirect
