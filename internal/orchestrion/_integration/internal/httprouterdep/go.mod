// A module outside dd-trace-go's paths that serves requests with an
// httprouter router, standing in for a third-party library. otelc must
// instrument routers used from dependencies, not only from the module being
// built.
module example.com/httprouterdep

go 1.26.0

require github.com/julienschmidt/httprouter v1.3.0
