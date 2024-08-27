module github.com/DataDog/dd-trace-go/v2/internal/traceprof/traceproftest

go 1.21

require (
	github.com/DataDog/dd-trace-go/contrib/google.golang.org/grpc/v2 v2.0.0-20240516153256-8d6fa2bea61d
	github.com/DataDog/dd-trace-go/contrib/julienschmidt/httprouter/v2 v2.0.0-20240516153256-8d6fa2bea61d
	github.com/DataDog/dd-trace-go/v2 v2.0.0-20240516153256-8d6fa2bea61d
	github.com/google/pprof v0.0.0-20230817174616-7a8ec2ada47b
	github.com/julienschmidt/httprouter v1.3.0
	github.com/stretchr/testify v1.8.4
	google.golang.org/grpc v1.64.0
)

require (
	github.com/DataDog/appsec-internal-go v1.5.0 // indirect
	github.com/DataDog/datadog-agent/pkg/obfuscate v0.52.1 // indirect
	github.com/DataDog/datadog-agent/pkg/remoteconfig/state v0.52.1 // indirect
	github.com/DataDog/datadog-go/v5 v5.5.0 // indirect
	github.com/DataDog/dd-trace-go/contrib/net/http/v2 v2.0.0-20240516153256-8d6fa2bea61d // indirect
	github.com/DataDog/go-libddwaf/v2 v2.4.2 // indirect
	github.com/DataDog/go-sqllexer v0.0.11 // indirect
	github.com/DataDog/go-tuf v1.1.0-0.5.2 // indirect
	github.com/DataDog/sketches-go v1.4.5 // indirect
	github.com/Microsoft/go-winio v0.6.2 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/ebitengine/purego v0.7.1 // indirect
	github.com/golang/protobuf v1.5.4 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/hashicorp/errwrap v1.1.0 // indirect
	github.com/hashicorp/go-multierror v1.1.1 // indirect
	github.com/outcaste-io/ristretto v0.2.3 // indirect
	github.com/philhofer/fwd v1.1.2 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/secure-systems-lab/go-securesystemslib v0.8.0 // indirect
	github.com/tinylib/msgp v1.1.9 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	golang.org/x/net v0.25.0 // indirect
	golang.org/x/sys v0.20.0 // indirect
	golang.org/x/text v0.15.0 // indirect
	golang.org/x/time v0.5.0 // indirect
	golang.org/x/xerrors v0.0.0-20231012003039-104605ab7028 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20240515191416-fc5f0ca64291 // indirect
	google.golang.org/protobuf v1.34.1 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/DataDog/dd-trace-go/contrib/google.golang.org/grpc/v2 => ../../../contrib/google.golang.org/grpc

replace github.com/DataDog/dd-trace-go/contrib/julienschmidt/httprouter/v2 => ../../../contrib/julienschmidt/httprouter

replace github.com/DataDog/dd-trace-go/contrib/net/http/v2 => ../../../contrib/net/http

replace github.com/DataDog/dd-trace-go/v2 => ../../..
