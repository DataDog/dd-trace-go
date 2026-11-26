// A module outside dd-trace-go's paths that creates franz-go clients, standing
// in for a third-party library. otelc must instrument call sites in
// dependencies, not only in the module being built.
module example.com/franzgodep

go 1.26.0

require github.com/twmb/franz-go v1.22.0

require (
	github.com/klauspost/compress v1.20.0 // indirect
	github.com/pierrec/lz4/v4 v4.1.30 // indirect
	github.com/twmb/franz-go/pkg/kmsg v1.14.0 // indirect
)
