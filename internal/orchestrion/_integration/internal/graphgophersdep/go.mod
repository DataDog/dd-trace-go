// A module outside dd-trace-go's paths that parses graphql-go schemas, standing
// in for a third-party library. otelc must rewrite call sites in dependencies,
// not only in the module being built.
module example.com/graphgophersdep

go 1.26.0

require github.com/graph-gophers/graphql-go v1.5.0
