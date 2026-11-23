// A module outside dd-trace-go's paths that creates chi routers, standing in
// for a third-party library. otelc must instrument call sites in dependencies,
// not only in the module being built.
module example.com/chidep

go 1.26.0

require (
	github.com/go-chi/chi v1.5.4
	github.com/go-chi/chi/v5 v5.2.2
)
