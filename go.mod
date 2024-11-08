module github.com/DataDog/dd-trace-go/v2

go 1.22.0

require (
	github.com/DataDog/appsec-internal-go v1.8.0
	github.com/DataDog/datadog-agent/pkg/obfuscate v0.48.0
	github.com/DataDog/datadog-agent/pkg/remoteconfig/state v0.57.0
	github.com/DataDog/datadog-go/v5 v5.3.0
	github.com/DataDog/go-libddwaf/v3 v3.4.0
	github.com/DataDog/gostackparse v0.7.0
	github.com/DataDog/sketches-go v1.4.5
	github.com/aws/aws-sdk-go-v2 v1.20.3
	github.com/aws/aws-sdk-go-v2/service/eventbridge v1.20.4
	github.com/aws/aws-sdk-go-v2/service/sns v1.21.4
	github.com/aws/aws-sdk-go-v2/service/sqs v1.24.4
	github.com/aws/smithy-go v1.14.2
	github.com/eapache/queue/v2 v2.0.0-20230407133247-75960ed334e4
	github.com/google/pprof v0.0.0-20230817174616-7a8ec2ada47b
	github.com/google/uuid v1.5.0
	github.com/hashicorp/go-secure-stdlib/parseutil v0.1.7
	github.com/mitchellh/mapstructure v1.5.0
	github.com/richardartoul/molecule v1.0.1-0.20240531184615-7ca0df43c0b3
	github.com/spaolacci/murmur3 v1.1.0
	github.com/stretchr/testify v1.9.0
	github.com/tinylib/msgp v1.2.1
	github.com/valyala/fasthttp v1.51.0
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.44.0
	go.opentelemetry.io/otel v1.20.0
	go.opentelemetry.io/otel/trace v1.20.0
	go.uber.org/atomic v1.11.0
	go.uber.org/goleak v1.3.0
	golang.org/x/mod v0.18.0
	golang.org/x/sys v0.23.0
	golang.org/x/time v0.3.0
	golang.org/x/xerrors v0.0.0-20231012003039-104605ab7028
	google.golang.org/grpc v1.57.1
	google.golang.org/protobuf v1.33.0
)

require (
	github.com/DataDog/go-tuf v1.1.0-0.5.2 // indirect
	github.com/Microsoft/go-winio v0.6.1 // indirect
	github.com/andybalholm/brotli v1.0.6 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.1.40 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.4.34 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.1.3 // indirect
	github.com/cespare/xxhash/v2 v2.2.0 // indirect
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/ebitengine/purego v0.6.0-alpha.5 // indirect
	github.com/felixge/httpsnoop v1.0.3 // indirect
	github.com/go-logr/logr v1.3.0 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/golang/protobuf v1.5.3 // indirect
	github.com/hashicorp/go-secure-stdlib/strutil v0.1.2 // indirect
	github.com/hashicorp/go-sockaddr v1.0.2 // indirect
	github.com/klauspost/compress v1.17.1 // indirect
	github.com/kr/pretty v0.3.0 // indirect
	github.com/outcaste-io/ristretto v0.2.3 // indirect
	github.com/philhofer/fwd v1.1.3-0.20240612014219-fbbf4953d986 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/rogpeppe/go-internal v1.9.0 // indirect
	github.com/ryanuber/go-glob v1.0.0 // indirect
	github.com/secure-systems-lab/go-securesystemslib v0.7.0 // indirect
	github.com/stretchr/objx v0.5.2 // indirect
	github.com/valyala/bytebufferpool v1.0.0 // indirect
	go.opentelemetry.io/otel/metric v1.20.0 // indirect
	golang.org/x/net v0.26.0 // indirect
	golang.org/x/sync v0.7.0 // indirect
	golang.org/x/text v0.16.0 // indirect
	golang.org/x/tools v0.22.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20230530153820-e85fd2cbaebc // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/DataDog/dd-trace-go/v2/internal/setup-smoke-test => ./internal/setup-smoke-test
