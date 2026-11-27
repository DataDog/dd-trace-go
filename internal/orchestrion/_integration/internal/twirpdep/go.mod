// A module outside dd-trace-go's paths that builds twirp server options the
// way generated code does, standing in for a third-party library. otelc must
// instrument twirp.ServerOptions literals in dependencies, not only in the
// module being built.
module example.com/twirpdep

go 1.26.0

require github.com/twitchtv/twirp v8.1.3+incompatible

require (
	github.com/pkg/errors v0.9.1 // indirect
	google.golang.org/protobuf v1.36.12 // indirect
)
