// A module outside dd-trace-go's paths that builds graphql-go schemas, standing
// in for a third-party library. otelc must rewrite call sites in dependencies,
// not only in the module being built.
module example.com/graphqlgodep

go 1.26.0

require github.com/graphql-go/graphql v0.8.1
