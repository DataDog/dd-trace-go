// A module outside dd-trace-go's paths that creates slog loggers, standing in
// for a third-party library. otelc must instrument call sites in dependencies,
// not only in the module being built.
module example.com/slogdep

go 1.26.0
