// A module outside dd-trace-go's paths that creates gorilla/mux routers,
// standing in for a third-party library. Routers created in dependencies must
// be traced too, not only those in the module being built.
module example.com/gorillamuxdep

go 1.26.0

require github.com/gorilla/mux v1.8.1
