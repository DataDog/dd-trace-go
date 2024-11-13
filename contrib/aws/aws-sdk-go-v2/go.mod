module github.com/DataDog/dd-trace-go/contrib/aws/aws-sdk-go-v2/v2

go 1.22.0

require (
	github.com/DataDog/dd-trace-go/v2 v2.0.0-beta.10
	github.com/aws/aws-sdk-go-v2 v1.20.3
	github.com/aws/aws-sdk-go-v2/config v1.18.21
	github.com/aws/aws-sdk-go-v2/feature/s3/manager v1.11.63
	github.com/aws/aws-sdk-go-v2/service/dynamodb v1.21.4
	github.com/aws/aws-sdk-go-v2/service/eventbridge v1.20.4
	github.com/aws/aws-sdk-go-v2/service/kinesis v1.18.4
	github.com/aws/aws-sdk-go-v2/service/s3 v1.32.0
	github.com/aws/aws-sdk-go-v2/service/sfn v1.19.4
	github.com/aws/aws-sdk-go-v2/service/sns v1.21.4
	github.com/aws/aws-sdk-go-v2/service/sqs v1.24.4
	github.com/aws/smithy-go v1.14.2
	github.com/stretchr/testify v1.9.0
)

require (
	github.com/DataDog/appsec-internal-go v1.8.0 // indirect
	github.com/DataDog/datadog-agent/pkg/obfuscate v0.48.0 // indirect
	github.com/DataDog/datadog-agent/pkg/remoteconfig/state v0.57.0 // indirect
	github.com/DataDog/datadog-go/v5 v5.3.0 // indirect
	github.com/DataDog/go-libddwaf/v3 v3.4.0 // indirect
	github.com/DataDog/go-tuf v1.1.0-0.5.2 // indirect
	github.com/DataDog/sketches-go v1.4.5 // indirect
	github.com/Microsoft/go-winio v0.6.1 // indirect
	github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream v1.4.13 // indirect
	github.com/aws/aws-sdk-go-v2/credentials v1.13.20 // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.13.2 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.1.40 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.4.34 // indirect
	github.com/aws/aws-sdk-go-v2/internal/ini v1.3.34 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.1.3 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.9.14 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/checksum v1.1.35 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/endpoint-discovery v1.7.34 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.9.34 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/s3shared v1.15.3 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.12.8 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.14.8 // indirect
	github.com/aws/aws-sdk-go-v2/service/sts v1.18.9 // indirect
	github.com/cespare/xxhash/v2 v2.2.0 // indirect
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/eapache/queue/v2 v2.0.0-20230407133247-75960ed334e4 // indirect
	github.com/ebitengine/purego v0.6.0-alpha.5 // indirect
	github.com/google/uuid v1.5.0 // indirect
	github.com/hashicorp/go-secure-stdlib/parseutil v0.1.7 // indirect
	github.com/hashicorp/go-secure-stdlib/strutil v0.1.2 // indirect
	github.com/hashicorp/go-sockaddr v1.0.2 // indirect
	github.com/jmespath/go-jmespath v0.4.0 // indirect
	github.com/mitchellh/mapstructure v1.5.0 // indirect
	github.com/outcaste-io/ristretto v0.2.3 // indirect
	github.com/philhofer/fwd v1.1.3-0.20240612014219-fbbf4953d986 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/ryanuber/go-glob v1.0.0 // indirect
	github.com/secure-systems-lab/go-securesystemslib v0.7.0 // indirect
	github.com/tinylib/msgp v1.2.1 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	golang.org/x/mod v0.18.0 // indirect
	golang.org/x/sync v0.7.0 // indirect
	golang.org/x/sys v0.23.0 // indirect
	golang.org/x/time v0.3.0 // indirect
	golang.org/x/tools v0.22.0 // indirect
	golang.org/x/xerrors v0.0.0-20231012003039-104605ab7028 // indirect
	google.golang.org/protobuf v1.33.0 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/DataDog/dd-trace-go/v2 => ../../..
