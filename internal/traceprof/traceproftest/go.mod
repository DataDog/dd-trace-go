module github.com/DataDog/dd-trace-go/v2/internal/traceprof/traceproftest

go 1.19

require (
	github.com/DataDog/dd-trace-go/v2 v2.0.0-20231220194510-f0d293776f63
	github.com/DataDog/dd-trace-go/v2/contrib/google.golang.org/grpc v0.0.0-20231220194510-f0d293776f63
	github.com/DataDog/dd-trace-go/v2/contrib/julienschmidt/httprouter v0.0.0-20231220194510-f0d293776f63
	github.com/google/pprof v0.0.0-20230817174616-7a8ec2ada47b
	github.com/julienschmidt/httprouter v1.3.0
	github.com/stretchr/testify v1.8.4
	google.golang.org/grpc v1.60.1
)

require (
	github.com/DataDog/appsec-internal-go v1.4.0 // indirect
	github.com/DataDog/datadog-agent/pkg/obfuscate v0.50.0 // indirect
	github.com/DataDog/datadog-agent/pkg/remoteconfig/state v0.50.0 // indirect
	github.com/DataDog/datadog-go/v5 v5.4.0 // indirect
	github.com/DataDog/dd-trace-go/v2/contrib/net/http v0.0.0-20231220194510-f0d293776f63 // indirect
	github.com/DataDog/go-libddwaf/v2 v2.2.3 // indirect
	github.com/DataDog/go-sqllexer v0.0.10 // indirect
	github.com/DataDog/go-tuf v1.0.2-0.5.2 // indirect
	github.com/DataDog/sketches-go v1.4.3 // indirect
	github.com/Microsoft/go-winio v0.6.1 // indirect
	github.com/cespare/xxhash/v2 v2.2.0 // indirect
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/ebitengine/purego v0.5.1 // indirect
	github.com/golang/protobuf v1.5.3 // indirect
	github.com/google/uuid v1.5.0 // indirect
	github.com/hashicorp/errwrap v1.1.0 // indirect
	github.com/hashicorp/go-multierror v1.1.1 // indirect
	github.com/outcaste-io/ristretto v0.2.3 // indirect
	github.com/philhofer/fwd v1.1.2 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/secure-systems-lab/go-securesystemslib v0.8.0 // indirect
	github.com/tinylib/msgp v1.1.9 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	go4.org/intern v0.0.0-20230525184215-6c62f75575cb // indirect
	go4.org/unsafe/assume-no-moving-gc v0.0.0-20231121144256-b99613f794b6 // indirect
	golang.org/x/mod v0.14.0 // indirect
	golang.org/x/net v0.19.0 // indirect
	golang.org/x/sys v0.15.0 // indirect
	golang.org/x/text v0.14.0 // indirect
	golang.org/x/time v0.5.0 // indirect
	golang.org/x/tools v0.16.1 // indirect
	golang.org/x/xerrors v0.0.0-20231012003039-104605ab7028 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20231212172506-995d672761c0 // indirect
	google.golang.org/protobuf v1.31.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	inet.af/netaddr v0.0.0-20230525184311-b8eac61e914a // indirect
)

replace github.com/DataDog/dd-trace-go/v2 => ../../..

replace github.com/DataDog/dd-trace-go/v2/contrib/google.golang.org/grpc => ../../../v2/contrib/google.golang.org/grpc

replace github.com/DataDog/dd-trace-go/v2/contrib/julienschmidt/httprouter => ../../../v2/contrib/julienschmidt/httprouter

replace github.com/DataDog/dd-trace-go/v2/contrib/net/http => ../../../v2/contrib/net/http
