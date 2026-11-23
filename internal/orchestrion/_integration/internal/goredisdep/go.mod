// A module outside dd-trace-go's paths that creates go-redis clients, standing
// in for a third-party library. otelc must instrument call sites in
// dependencies, not only in the module being built.
module example.com/goredisdep

go 1.26.0

require (
	github.com/go-redis/redis v6.15.9+incompatible
	github.com/go-redis/redis/v7 v7.4.1
	github.com/go-redis/redis/v8 v8.11.5
	github.com/redis/go-redis/v9 v9.7.3
)

require (
	github.com/cespare/xxhash/v2 v2.2.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
)
