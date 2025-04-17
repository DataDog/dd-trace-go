module github.com/DataDog/dd-trace-go/contrib/traefik/cmd/plugin

go 1.22.0

toolchain go1.24.2

require (
	github.com/envoyproxy/go-control-plane/envoy v1.32.4
	github.com/http-wasm/http-wasm-guest-tinygo v0.2.0
	github.com/puzpuzpuz/xsync/v3 v3.5.1
	github.com/stealthrocket/net v0.2.1
	google.golang.org/grpc v1.71.1
)

require (
	github.com/cncf/xds/go v0.0.0-20241223141626-cff3c89139a3 // indirect
	github.com/envoyproxy/protoc-gen-validate v1.2.1 // indirect
	github.com/planetscale/vtprotobuf v0.6.1-0.20240319094008-0393e58bdf10 // indirect
	golang.org/x/net v0.34.0 // indirect
	golang.org/x/sys v0.29.0 // indirect
	golang.org/x/text v0.21.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20250115164207-1a7da9e5054f // indirect
	google.golang.org/protobuf v1.36.4 // indirect
)

replace github.com/http-wasm/http-wasm-guest-tinygo => github.com/traefik/http-wasm-guest-tinygo v0.0.0-20240913140402-af96219ffea5
