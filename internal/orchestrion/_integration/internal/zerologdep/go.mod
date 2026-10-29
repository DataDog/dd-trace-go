// A module outside dd-trace-go's paths that builds zerolog loggers, standing in
// for a third-party library. otelc must instrument call sites in dependencies,
// not only in the module being built.
module example.com/zerologdep

go 1.26.0

require github.com/rs/zerolog v1.35.1

require (
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	golang.org/x/sys v0.29.0 // indirect
)
