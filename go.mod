module github.com/DataDog/dd-trace-go/v2

go 1.22.0

require (
	github.com/DataDog/appsec-internal-go v1.9.0
	github.com/DataDog/datadog-agent/pkg/obfuscate v0.59.0
	github.com/DataDog/datadog-agent/pkg/proto v0.59.0
	github.com/DataDog/datadog-agent/pkg/remoteconfig/state v0.59.0
	github.com/DataDog/datadog-agent/pkg/trace v0.59.0
	github.com/DataDog/datadog-go/v5 v5.5.0
	github.com/DataDog/go-libddwaf/v3 v3.5.1
	github.com/DataDog/go-runtime-metrics-internal v0.0.4-0.20241206090539-a14610dc22b6
	github.com/DataDog/gostackparse v0.7.0
	github.com/DataDog/sketches-go v1.4.6
	github.com/aws/aws-sdk-go-v2 v1.20.3
	github.com/aws/aws-sdk-go-v2/service/eventbridge v1.20.4
	github.com/aws/aws-sdk-go-v2/service/sfn v1.19.4
	github.com/aws/aws-sdk-go-v2/service/sns v1.21.4
	github.com/aws/aws-sdk-go-v2/service/sqs v1.24.4
	github.com/aws/smithy-go v1.22.1
	github.com/eapache/queue/v2 v2.0.0-20230407133247-75960ed334e4
	github.com/google/pprof v0.0.0-20230817174616-7a8ec2ada47b
	github.com/google/uuid v1.6.0
	github.com/hashicorp/go-secure-stdlib/parseutil v0.1.8
	github.com/mitchellh/mapstructure v1.5.1-0.20231216201459-8508981c8b6c
	github.com/richardartoul/molecule v1.0.1-0.20240531184615-7ca0df43c0b3
	github.com/spaolacci/murmur3 v1.1.0
	github.com/stretchr/testify v1.10.0
	github.com/tinylib/msgp v1.2.2
	github.com/valyala/fasthttp v1.51.0
	go.opentelemetry.io/collector/pdata/pprofile v0.104.0
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.49.0
	go.opentelemetry.io/otel v1.31.0
	go.opentelemetry.io/otel/trace v1.31.0
	go.uber.org/atomic v1.11.0
	go.uber.org/goleak v1.3.0
	golang.org/x/mod v0.22.0
	golang.org/x/sys v0.28.0
	golang.org/x/time v0.7.0
	golang.org/x/xerrors v0.0.0-20240903120638-7835f813f4da
	google.golang.org/grpc v1.69.0
	google.golang.org/protobuf v1.35.1
)

require (
	github.com/DataDog/datadog-agent/pkg/util/log v0.59.0 // indirect
	github.com/DataDog/datadog-agent/pkg/util/scrubber v0.59.0 // indirect
	github.com/DataDog/go-sqllexer v0.0.16 // indirect
	github.com/DataDog/go-tuf v1.1.0-0.5.2 // indirect
	github.com/DataDog/opentelemetry-mapping-go/pkg/otlp/attributes v0.20.0 // indirect
	github.com/Microsoft/go-winio v0.6.2 // indirect
	github.com/andybalholm/brotli v1.0.6 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.1.40 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.4.34 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.1.3 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/cihub/seelog v0.0.0-20170130134532-f561c5e57575 // indirect
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/ebitengine/purego v0.8.0 // indirect
	github.com/felixge/httpsnoop v1.0.4 // indirect
	github.com/go-logr/logr v1.4.2 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/go-ole/go-ole v1.2.6 // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/golang/protobuf v1.5.4 // indirect
	github.com/hashicorp/go-secure-stdlib/strutil v0.1.2 // indirect
	github.com/hashicorp/go-sockaddr v1.0.7 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/klauspost/compress v1.17.11 // indirect
	github.com/lufia/plan9stats v0.0.0-20220913051719-115f729f3c8c // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/outcaste-io/ristretto v0.2.3 // indirect
	github.com/philhofer/fwd v1.1.3-0.20240916144458-20a13a1f6b7c // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/power-devops/perfstat v0.0.0-20220216144756-c35f1ee13d7c // indirect
	github.com/ryanuber/go-glob v1.0.0 // indirect
	github.com/secure-systems-lab/go-securesystemslib v0.8.0 // indirect
	github.com/shirou/gopsutil/v3 v3.24.4 // indirect
	github.com/shoenig/go-m1cpu v0.1.6 // indirect
	github.com/stretchr/objx v0.5.2 // indirect
	github.com/tklauser/go-sysconf v0.3.12 // indirect
	github.com/tklauser/numcpus v0.6.1 // indirect
	github.com/valyala/bytebufferpool v1.0.0 // indirect
	github.com/yusufpapurcu/wmi v1.2.4 // indirect
	go.opentelemetry.io/collector/component v0.104.0 // indirect
	go.opentelemetry.io/collector/config/configtelemetry v0.104.0 // indirect
	go.opentelemetry.io/collector/pdata v1.11.0 // indirect
	go.opentelemetry.io/collector/semconv v0.104.0 // indirect
	go.opentelemetry.io/otel/metric v1.31.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.uber.org/zap v1.27.0 // indirect
	golang.org/x/net v0.33.0 // indirect
	golang.org/x/text v0.21.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20241015192408-796eee8c2d53 // indirect
	gopkg.in/ini.v1 v1.67.0 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)
