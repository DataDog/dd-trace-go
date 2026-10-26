// A module outside dd-trace-go's paths that creates echo instances, standing in
// for a third-party library. otelc must instrument call sites in dependencies,
// not only in the module being built.
module example.com/echodep

go 1.26.0

require (
	github.com/labstack/echo/v4 v4.15.3
	github.com/labstack/echo/v5 v5.2.0
)

require (
	github.com/labstack/gommon v0.5.0 // indirect
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.22 // indirect
	github.com/valyala/bytebufferpool v1.0.0 // indirect
	github.com/valyala/fasttemplate v1.2.2 // indirect
	golang.org/x/crypto v0.50.0 // indirect
	golang.org/x/net v0.53.0 // indirect
	golang.org/x/sys v0.43.0 // indirect
	golang.org/x/text v0.36.0 // indirect
)
