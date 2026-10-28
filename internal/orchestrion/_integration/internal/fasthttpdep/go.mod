// A module outside dd-trace-go's paths that serves fasthttp, standing in for a
// third-party library.
module example.com/fasthttpdep

go 1.26.0

require github.com/valyala/fasthttp v1.75.0

require (
	github.com/klauspost/compress v1.20.1 // indirect
	github.com/molecule-man/go-brrr v1.1.1 // indirect
	github.com/valyala/bytebufferpool v1.0.0 // indirect
)
