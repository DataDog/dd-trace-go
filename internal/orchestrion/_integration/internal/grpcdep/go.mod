// A module outside dd-trace-go's paths that creates gRPC clients and servers,
// standing in for a third-party library. otelc must rewrite call sites in
// dependencies, not only in the module being built.
module example.com/grpcdep

go 1.26.0

require google.golang.org/grpc v1.83.2

require (
	golang.org/x/net v0.58.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.41.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260526163538-3dc84a4a5aaa // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)
