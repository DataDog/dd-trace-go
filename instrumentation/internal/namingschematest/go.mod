module github.com/DataDog/dd-trace-go/instrumentation/internal/namingschematest

go 1.21

require (
	cloud.google.com/go/pubsub v1.41.0
	github.com/99designs/gqlgen v0.17.49
	github.com/DataDog/dd-trace-go/contrib/99designs/gqlgen/v2 v2.0.0-00010101000000-000000000000
	github.com/DataDog/dd-trace-go/contrib/aws/aws-sdk-go-v2/v2 v2.0.0-00010101000000-000000000000
	github.com/DataDog/dd-trace-go/contrib/aws/aws-sdk-go/v2 v2.0.0-00010101000000-000000000000
	github.com/DataDog/dd-trace-go/contrib/bradfitz/gomemcache/v2 v2.0.0-00010101000000-000000000000
	github.com/DataDog/dd-trace-go/contrib/cloud.google.com/go/pubsub.v1/v2 v2.0.0-00010101000000-000000000000
	github.com/DataDog/dd-trace-go/contrib/confluentinc/confluent-kafka-go/kafka.v2/v2 v2.0.0-00010101000000-000000000000
	github.com/DataDog/dd-trace-go/contrib/confluentinc/confluent-kafka-go/kafka/v2 v2.0.0-00010101000000-000000000000
	github.com/DataDog/dd-trace-go/contrib/database/sql/v2 v2.0.0-00010101000000-000000000000
	github.com/DataDog/dd-trace-go/contrib/dimfeld/httptreemux.v5/v2 v2.0.0-00010101000000-000000000000
	github.com/DataDog/dd-trace-go/contrib/elastic/go-elasticsearch.v6/v2 v2.0.0-00010101000000-000000000000
	github.com/DataDog/dd-trace-go/contrib/emicklei/go-restful.v3/v2 v2.0.0-00010101000000-000000000000
	github.com/DataDog/dd-trace-go/contrib/net/http/v2 v2.0.0-20240516153256-8d6fa2bea61d
	github.com/DataDog/dd-trace-go/contrib/urfave/negroni/v2 v2.0.0-00010101000000-000000000000
	github.com/DataDog/dd-trace-go/v2 v2.0.0-beta.2
	github.com/aws/aws-sdk-go v1.54.20
	github.com/aws/aws-sdk-go-v2 v1.30.3
	github.com/aws/aws-sdk-go-v2/config v1.27.27
	github.com/aws/aws-sdk-go-v2/service/ec2 v1.171.0
	github.com/aws/aws-sdk-go-v2/service/s3 v1.58.2
	github.com/aws/aws-sdk-go-v2/service/sns v1.31.3
	github.com/aws/aws-sdk-go-v2/service/sqs v1.34.3
	github.com/bradfitz/gomemcache v0.0.0-20230905024940-24af94b03874
	github.com/confluentinc/confluent-kafka-go v1.9.2
	github.com/confluentinc/confluent-kafka-go/v2 v2.5.0
	github.com/denisenkom/go-mssqldb v0.12.3
	github.com/elastic/go-elasticsearch/v8 v8.14.0
	github.com/emicklei/go-restful/v3 v3.11.0
	github.com/go-sql-driver/mysql v1.8.1
	github.com/lib/pq v1.10.9
	github.com/stretchr/testify v1.9.0
	github.com/urfave/negroni v1.0.0
	google.golang.org/api v0.191.0
	google.golang.org/grpc v1.64.1
)

require (
	cloud.google.com/go v0.115.0 // indirect
	cloud.google.com/go/auth v0.7.3 // indirect
	cloud.google.com/go/auth/oauth2adapt v0.2.3 // indirect
	cloud.google.com/go/compute/metadata v0.5.0 // indirect
	cloud.google.com/go/iam v1.1.12 // indirect
	filippo.io/edwards25519 v1.1.0 // indirect
	github.com/DataDog/appsec-internal-go v1.7.0 // indirect
	github.com/DataDog/datadog-agent/pkg/obfuscate v0.52.1 // indirect
	github.com/DataDog/datadog-agent/pkg/remoteconfig/state v0.52.1 // indirect
	github.com/DataDog/datadog-go/v5 v5.5.0 // indirect
	github.com/DataDog/go-libddwaf/v2 v2.4.2 // indirect
	github.com/DataDog/go-sqllexer v0.0.12 // indirect
	github.com/DataDog/go-tuf v1.1.0-0.5.2 // indirect
	github.com/DataDog/sketches-go v1.4.6 // indirect
	github.com/Microsoft/go-winio v0.6.2 // indirect
	github.com/agnivade/levenshtein v1.1.1 // indirect
	github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream v1.6.3 // indirect
	github.com/aws/aws-sdk-go-v2/credentials v1.17.27 // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.16.11 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.3.15 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.6.15 // indirect
	github.com/aws/aws-sdk-go-v2/internal/ini v1.8.0 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.3.15 // indirect
	github.com/aws/aws-sdk-go-v2/service/dynamodb v1.34.3 // indirect
	github.com/aws/aws-sdk-go-v2/service/eventbridge v1.33.3 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.11.3 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/checksum v1.3.17 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/endpoint-discovery v1.9.16 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.11.17 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/s3shared v1.17.15 // indirect
	github.com/aws/aws-sdk-go-v2/service/kinesis v1.29.3 // indirect
	github.com/aws/aws-sdk-go-v2/service/sfn v1.29.3 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.22.4 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.26.4 // indirect
	github.com/aws/aws-sdk-go-v2/service/sts v1.30.3 // indirect
	github.com/aws/smithy-go v1.20.3 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/dimfeld/httptreemux/v5 v5.5.0 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/ebitengine/purego v0.7.1 // indirect
	github.com/elastic/elastic-transport-go/v8 v8.6.0 // indirect
	github.com/felixge/httpsnoop v1.0.4 // indirect
	github.com/go-logr/logr v1.4.2 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/golang-sql/civil v0.0.0-20220223132316-b832511892a9 // indirect
	github.com/golang-sql/sqlexp v0.1.0 // indirect
	github.com/golang/groupcache v0.0.0-20210331224755-41bb18bfe9da // indirect
	github.com/google/go-cmp v0.6.0 // indirect
	github.com/google/s2a-go v0.1.8 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/googleapis/enterprise-certificate-proxy v0.3.2 // indirect
	github.com/googleapis/gax-go/v2 v2.13.0 // indirect
	github.com/gorilla/websocket v1.5.0 // indirect
	github.com/hashicorp/errwrap v1.1.0 // indirect
	github.com/hashicorp/go-multierror v1.1.1 // indirect
	github.com/hashicorp/golang-lru/v2 v2.0.7 // indirect
	github.com/jmespath/go-jmespath v0.4.0 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/mitchellh/mapstructure v1.5.0 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/outcaste-io/ristretto v0.2.3 // indirect
	github.com/philhofer/fwd v1.1.3-0.20240612014219-fbbf4953d986 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/secure-systems-lab/go-securesystemslib v0.8.0 // indirect
	github.com/sosodev/duration v1.3.1 // indirect
	github.com/tinylib/msgp v1.2.0 // indirect
	github.com/vektah/gqlparser/v2 v2.5.16 // indirect
	go.einride.tech/aip v0.67.1 // indirect
	go.opencensus.io v0.24.0 // indirect
	go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc v0.49.0 // indirect
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.49.0 // indirect
	go.opentelemetry.io/otel v1.24.0 // indirect
	go.opentelemetry.io/otel/metric v1.24.0 // indirect
	go.opentelemetry.io/otel/sdk v1.24.0 // indirect
	go.opentelemetry.io/otel/trace v1.24.0 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	golang.org/x/crypto v0.25.0 // indirect
	golang.org/x/net v0.27.0 // indirect
	golang.org/x/oauth2 v0.22.0 // indirect
	golang.org/x/sync v0.8.0 // indirect
	golang.org/x/sys v0.22.0 // indirect
	golang.org/x/text v0.16.0 // indirect
	golang.org/x/time v0.6.0 // indirect
	golang.org/x/xerrors v0.0.0-20240716161551-93cc26a95ae9 // indirect
	google.golang.org/genproto v0.0.0-20240730163845-b1a4ccb954bf // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20240725223205-93522f1f2a9f // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20240730163845-b1a4ccb954bf // indirect
	google.golang.org/protobuf v1.34.2 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/DataDog/dd-trace-go/contrib/99designs/gqlgen/v2 => ../../../contrib/99designs/gqlgen

replace github.com/DataDog/dd-trace-go/contrib/aws/aws-sdk-go-v2/v2 => ../../../contrib/aws/aws-sdk-go-v2

replace github.com/DataDog/dd-trace-go/contrib/aws/aws-sdk-go/v2 => ../../../contrib/aws/aws-sdk-go

replace github.com/DataDog/dd-trace-go/contrib/bradfitz/gomemcache/v2 => ../../../contrib/bradfitz/gomemcache

replace github.com/DataDog/dd-trace-go/contrib/cloud.google.com/go/pubsub.v1/v2 => ../../../contrib/cloud.google.com/go/pubsub.v1

replace github.com/DataDog/dd-trace-go/contrib/confluentinc/confluent-kafka-go/kafka.v2/v2 => ../../../contrib/confluentinc/confluent-kafka-go/kafka.v2

replace github.com/DataDog/dd-trace-go/contrib/confluentinc/confluent-kafka-go/kafka/v2 => ../../../contrib/confluentinc/confluent-kafka-go/kafka

replace github.com/DataDog/dd-trace-go/contrib/database/sql/v2 => ../../../contrib/database/sql

replace github.com/DataDog/dd-trace-go/contrib/dimfeld/httptreemux.v5/v2 => ../../../contrib/dimfeld/httptreemux.v5

replace github.com/DataDog/dd-trace-go/contrib/elastic/go-elasticsearch.v6/v2 => ../../../contrib/elastic/go-elasticsearch.v6

replace github.com/DataDog/dd-trace-go/contrib/emicklei/go-restful.v3/v2 => ../../../contrib/emicklei/go-restful.v3

replace github.com/DataDog/dd-trace-go/contrib/net/http/v2 => ../../../contrib/net/http

replace github.com/DataDog/dd-trace-go/contrib/urfave/negroni/v2 => ../../../contrib/urfave/negroni

replace github.com/DataDog/dd-trace-go/v2 => ../../..
