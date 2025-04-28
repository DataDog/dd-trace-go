module gopkg.in/DataDog/dd-trace-go.v1

go 1.23.0

godebug x509negativeserial=1

require (
	cloud.google.com/go/pubsub v1.37.0
	github.com/99designs/gqlgen v0.17.36
	github.com/DataDog/dd-trace-go/contrib/99designs/gqlgen/v2 v2.0.0-rc.13
	github.com/DataDog/dd-trace-go/contrib/IBM/sarama/v2 v2.0.0-rc.13
	github.com/DataDog/dd-trace-go/contrib/Shopify/sarama/v2 v2.0.0-rc.13
	github.com/DataDog/dd-trace-go/contrib/aws/aws-sdk-go-v2/v2 v2.0.0-rc.13
	github.com/DataDog/dd-trace-go/contrib/aws/aws-sdk-go/v2 v2.0.0-rc.13
	github.com/DataDog/dd-trace-go/contrib/bradfitz/gomemcache/v2 v2.0.0-rc.13
	github.com/DataDog/dd-trace-go/contrib/cloud.google.com/go/pubsub.v1/v2 v2.0.0-rc.13
	github.com/DataDog/dd-trace-go/contrib/confluentinc/confluent-kafka-go/kafka.v2/v2 v2.0.0-rc.13
	github.com/DataDog/dd-trace-go/contrib/confluentinc/confluent-kafka-go/kafka/v2 v2.0.0-rc.13
	github.com/DataDog/dd-trace-go/contrib/database/sql/v2 v2.0.0-rc.13
	github.com/DataDog/dd-trace-go/contrib/dimfeld/httptreemux.v5/v2 v2.0.0-rc.13
	github.com/DataDog/dd-trace-go/contrib/elastic/go-elasticsearch.v6/v2 v2.0.0-rc.13
	github.com/DataDog/dd-trace-go/contrib/emicklei/go-restful.v3/v2 v2.0.0-rc.13
	github.com/DataDog/dd-trace-go/contrib/envoyproxy/go-control-plane/v2 v2.0.0-rc.13
	github.com/DataDog/dd-trace-go/contrib/gin-gonic/gin/v2 v2.0.0-rc.13
	github.com/DataDog/dd-trace-go/contrib/globalsign/mgo/v2 v2.0.0-rc.13
	github.com/DataDog/dd-trace-go/contrib/go-chi/chi.v5/v2 v2.0.0-rc.13
	github.com/DataDog/dd-trace-go/contrib/go-chi/chi/v2 v2.0.0-rc.13
	github.com/DataDog/dd-trace-go/contrib/go-pg/pg.v10/v2 v2.0.0-rc.13
	github.com/DataDog/dd-trace-go/contrib/go-redis/redis.v7/v2 v2.0.0-rc.13
	github.com/DataDog/dd-trace-go/contrib/go-redis/redis.v8/v2 v2.0.0-rc.13
	github.com/DataDog/dd-trace-go/contrib/go-redis/redis/v2 v2.0.0-rc.13
	github.com/DataDog/dd-trace-go/contrib/go.mongodb.org/mongo-driver/v2 v2.0.0-rc.13
	github.com/DataDog/dd-trace-go/contrib/gocql/gocql/v2 v2.0.0-rc.13
	github.com/DataDog/dd-trace-go/contrib/gofiber/fiber.v2/v2 v2.0.0-rc.13
	github.com/DataDog/dd-trace-go/contrib/gomodule/redigo/v2 v2.0.0-rc.13
	github.com/DataDog/dd-trace-go/contrib/google.golang.org/api/v2 v2.0.0-rc.13
	github.com/DataDog/dd-trace-go/contrib/google.golang.org/grpc/v2 v2.0.0-rc.13
	github.com/DataDog/dd-trace-go/contrib/gorilla/mux/v2 v2.0.0-rc.13
	github.com/DataDog/dd-trace-go/contrib/gorm.io/gorm.v1/v2 v2.0.0-rc.13
	github.com/DataDog/dd-trace-go/contrib/graph-gophers/graphql-go/v2 v2.0.0-rc.13
	github.com/DataDog/dd-trace-go/contrib/graphql-go/graphql/v2 v2.0.0-rc.13
	github.com/DataDog/dd-trace-go/contrib/hashicorp/consul/v2 v2.0.0-rc.13
	github.com/DataDog/dd-trace-go/contrib/hashicorp/vault/v2 v2.0.0-rc.13
	github.com/DataDog/dd-trace-go/contrib/jackc/pgx.v5/v2 v2.0.0-rc.13
	github.com/DataDog/dd-trace-go/contrib/jmoiron/sqlx/v2 v2.0.0-rc.13
	github.com/DataDog/dd-trace-go/contrib/julienschmidt/httprouter/v2 v2.0.0-rc.13
	github.com/DataDog/dd-trace-go/contrib/k8s.io/client-go/v2 v2.0.0-rc.13
	github.com/DataDog/dd-trace-go/contrib/labstack/echo.v4/v2 v2.0.0-rc.13
	github.com/DataDog/dd-trace-go/contrib/log/slog/v2 v2.0.0-rc.13
	github.com/DataDog/dd-trace-go/contrib/miekg/dns/v2 v2.0.0-rc.13
	github.com/DataDog/dd-trace-go/contrib/net/http/v2 v2.0.0-rc.13
	github.com/DataDog/dd-trace-go/contrib/olivere/elastic.v5/v2 v2.0.0-rc.13
	github.com/DataDog/dd-trace-go/contrib/redis/go-redis.v9/v2 v2.0.0-rc.13
	github.com/DataDog/dd-trace-go/contrib/redis/rueidis/v2 v2.0.0-rc.13
	github.com/DataDog/dd-trace-go/contrib/segmentio/kafka-go/v2 v2.0.0-rc.13
	github.com/DataDog/dd-trace-go/contrib/sirupsen/logrus/v2 v2.0.0-rc.13
	github.com/DataDog/dd-trace-go/contrib/syndtr/goleveldb/v2 v2.0.0-rc.13
	github.com/DataDog/dd-trace-go/contrib/tidwall/buntdb/v2 v2.0.0-rc.13
	github.com/DataDog/dd-trace-go/contrib/twitchtv/twirp/v2 v2.0.0-rc.13
	github.com/DataDog/dd-trace-go/contrib/uptrace/bun/v2 v2.0.0-rc.13
	github.com/DataDog/dd-trace-go/contrib/urfave/negroni/v2 v2.0.0-rc.13
	github.com/DataDog/dd-trace-go/contrib/valkey-io/valkey-go/v2 v2.0.0-rc.13
	github.com/DataDog/dd-trace-go/contrib/valyala/fasthttp/v2 v2.0.0-rc.13
	github.com/DataDog/dd-trace-go/instrumentation/testutils/grpc/v2 v2.0.0-rc.13
	github.com/DataDog/dd-trace-go/orchestrion/all/v2 v2.0.0-rc.13
	github.com/DataDog/dd-trace-go/v2 v2.1.0-dev.1
	github.com/DataDog/gostackparse v0.7.0
	github.com/IBM/sarama v1.40.0
	github.com/Shopify/sarama v1.38.1
	github.com/aws/aws-sdk-go v1.44.327
	github.com/aws/aws-sdk-go-v2 v1.22.1
	github.com/aws/aws-sdk-go-v2/config v1.22.1
	github.com/aws/aws-sdk-go-v2/service/dynamodb v1.21.4
	github.com/aws/aws-sdk-go-v2/service/eventbridge v1.20.4
	github.com/aws/aws-sdk-go-v2/service/kinesis v1.18.4
	github.com/aws/aws-sdk-go-v2/service/s3 v1.32.0
	github.com/aws/aws-sdk-go-v2/service/sfn v1.19.4
	github.com/aws/aws-sdk-go-v2/service/sns v1.21.4
	github.com/aws/aws-sdk-go-v2/service/sqs v1.24.4
	github.com/bradfitz/gomemcache v0.0.0-20230611145640-acc696258285
	github.com/confluentinc/confluent-kafka-go v1.9.2
	github.com/confluentinc/confluent-kafka-go/v2 v2.4.0
	github.com/denisenkom/go-mssqldb v0.11.0
	github.com/dimfeld/httptreemux/v5 v5.5.0
	github.com/elastic/go-elasticsearch/v6 v6.8.5
	github.com/elastic/go-elasticsearch/v7 v7.17.1
	github.com/elastic/go-elasticsearch/v8 v8.4.0
	github.com/emicklei/go-restful v2.16.0+incompatible
	github.com/emicklei/go-restful/v3 v3.11.0
	github.com/envoyproxy/go-control-plane/envoy v1.32.4
	github.com/garyburd/redigo v1.6.4
	github.com/gin-gonic/gin v1.9.1
	github.com/globalsign/mgo v0.0.0-20181015135952-eeefdecb41b8
	github.com/go-chi/chi v1.5.4
	github.com/go-chi/chi/v5 v5.0.10
	github.com/go-pg/pg/v10 v10.11.1
	github.com/go-redis/redis v6.15.9+incompatible
	github.com/go-redis/redis/v7 v7.4.1
	github.com/go-redis/redis/v8 v8.11.5
	github.com/go-sql-driver/mysql v1.6.0
	github.com/gocql/gocql v1.6.0
	github.com/gofiber/fiber/v2 v2.52.5
	github.com/gomodule/redigo v1.8.9
	github.com/google/pprof v0.0.0-20250403155104-27863c87afa6
	github.com/google/uuid v1.6.0
	github.com/gorilla/mux v1.8.1
	github.com/graph-gophers/graphql-go v1.5.0
	github.com/graphql-go/graphql v0.8.1
	github.com/graphql-go/handler v0.2.3
	github.com/hashicorp/consul/api v1.24.0
	github.com/hashicorp/vault/api v1.9.2
	github.com/jackc/pgx/v5 v5.6.0
	github.com/jinzhu/gorm v1.9.16
	github.com/jmoiron/sqlx v1.3.5
	github.com/julienschmidt/httprouter v1.3.0
	github.com/labstack/echo v3.3.10+incompatible
	github.com/labstack/echo/v4 v4.11.1
	github.com/lib/pq v1.10.2
	github.com/mattn/go-sqlite3 v1.14.18
	github.com/microsoft/go-mssqldb v0.21.0
	github.com/miekg/dns v1.1.55
	github.com/opentracing/opentracing-go v1.2.0
	github.com/redis/go-redis/v9 v9.1.0
	github.com/redis/rueidis v1.0.56
	github.com/segmentio/kafka-go v0.4.42
	github.com/sirupsen/logrus v1.9.3
	github.com/stretchr/testify v1.10.0
	github.com/syndtr/goleveldb v1.0.1-0.20220721030215-126854af5e6d
	github.com/tidwall/buntdb v1.3.0
	github.com/tinylib/msgp v1.2.5
	github.com/twitchtv/twirp v8.1.3+incompatible
	github.com/uptrace/bun v1.1.17
	github.com/uptrace/bun/dialect/sqlitedialect v1.1.17
	github.com/urfave/negroni v1.0.0
	github.com/valkey-io/valkey-go v1.0.56
	github.com/valyala/fasthttp v1.51.0
	github.com/vektah/gqlparser/v2 v2.5.16
	github.com/zenazn/goji v1.0.1
	go.mongodb.org/mongo-driver v1.12.1
	go.opentelemetry.io/collector/pdata/pprofile v0.123.0
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.49.0
	go.opentelemetry.io/otel v1.35.0
	go.opentelemetry.io/otel/trace v1.35.0
	go.uber.org/goleak v1.3.0
	golang.org/x/sync v0.13.0
	golang.org/x/sys v0.32.0
	golang.org/x/time v0.11.0
	golang.org/x/tools v0.32.0
	google.golang.org/api v0.169.0
	google.golang.org/grpc v1.71.1
	google.golang.org/protobuf v1.36.6
	gopkg.in/jinzhu/gorm.v1 v1.9.2
	gopkg.in/olivere/elastic.v3 v3.0.75
	gopkg.in/olivere/elastic.v5 v5.0.84
	gorm.io/driver/mysql v1.0.1
	gorm.io/driver/postgres v1.5.5
	gorm.io/driver/sqlserver v1.4.2
	gorm.io/gorm v1.25.12
	k8s.io/apimachinery v0.32.3
	k8s.io/client-go v0.31.4
	modernc.org/sqlite v1.28.0
)

require (
	github.com/Masterminds/semver/v3 v3.3.1 // indirect
	golang.org/x/mod v0.24.0 // indirect
	golang.org/x/oauth2 v0.25.0 // indirect
	golang.org/x/xerrors v0.0.0-20240903120638-7835f813f4da // indirect
)

require (
	cloud.google.com/go v0.112.1 // indirect
	cloud.google.com/go/compute/metadata v0.6.0 // indirect
	cloud.google.com/go/iam v1.1.7 // indirect
	github.com/DataDog/appsec-internal-go v1.11.2 // indirect
	github.com/DataDog/datadog-agent/comp/core/tagger/origindetection v0.64.2 // indirect
	github.com/DataDog/datadog-agent/pkg/obfuscate v0.64.2 // indirect
	github.com/DataDog/datadog-agent/pkg/proto v0.64.2 // indirect
	github.com/DataDog/datadog-agent/pkg/remoteconfig/state v0.64.2 // indirect
	github.com/DataDog/datadog-agent/pkg/trace v0.64.2 // indirect
	github.com/DataDog/datadog-agent/pkg/util/log v0.64.2 // indirect
	github.com/DataDog/datadog-agent/pkg/util/scrubber v0.64.2 // indirect
	github.com/DataDog/datadog-agent/pkg/version v0.64.2 // indirect
	github.com/DataDog/datadog-go/v5 v5.6.0 // indirect
	github.com/DataDog/go-libddwaf/v3 v3.5.4 // indirect
	github.com/DataDog/go-runtime-metrics-internal v0.0.4-0.20250319104955-81009b9bad14 // indirect
	github.com/DataDog/go-sqllexer v0.1.3 // indirect
	github.com/DataDog/go-tuf v1.1.0-0.5.2 // indirect
	github.com/DataDog/opentelemetry-mapping-go/pkg/otlp/attributes v0.26.0 // indirect
	github.com/DataDog/orchestrion v1.4.0-rc.3 // indirect
	github.com/DataDog/sketches-go v1.4.7 // indirect
	github.com/Microsoft/go-winio v0.6.2 // indirect
	github.com/agnivade/levenshtein v1.1.1 // indirect
	github.com/andybalholm/brotli v1.0.6 // indirect
	github.com/armon/go-metrics v0.4.1 // indirect
	github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream v1.4.13 // indirect
	github.com/aws/aws-sdk-go-v2/credentials v1.15.1 // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.14.2 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.2.1 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.5.1 // indirect
	github.com/aws/aws-sdk-go-v2/internal/ini v1.5.0 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.1.3 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.9.14 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/checksum v1.1.35 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/endpoint-discovery v1.7.34 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.10.1 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/s3shared v1.15.3 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.17.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.19.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/sts v1.25.0 // indirect
	github.com/aws/smithy-go v1.16.0 // indirect
	github.com/aymanbagabas/go-osc52/v2 v2.0.1 // indirect
	github.com/blakesmith/ar v0.0.0-20190502131153-809d4375e1fb // indirect
	github.com/bytedance/sonic v1.12.0 // indirect
	github.com/bytedance/sonic/loader v0.2.0 // indirect
	github.com/cenkalti/backoff/v3 v3.2.2 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/charmbracelet/colorprofile v0.3.0 // indirect
	github.com/charmbracelet/lipgloss v1.1.0 // indirect
	github.com/charmbracelet/x/ansi v0.8.0 // indirect
	github.com/charmbracelet/x/cellbuf v0.0.13 // indirect
	github.com/charmbracelet/x/term v0.2.1 // indirect
	github.com/cihub/seelog v0.0.0-20170130134532-f561c5e57575 // indirect
	github.com/cloudwego/base64x v0.1.4 // indirect
	github.com/cloudwego/iasm v0.2.0 // indirect
	github.com/cncf/xds/go v0.0.0-20241223141626-cff3c89139a3 // indirect
	github.com/cpuguy83/go-md2man/v2 v2.0.6 // indirect
	github.com/dave/dst v0.27.3 // indirect
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/eapache/go-resiliency v1.4.0 // indirect
	github.com/eapache/go-xerial-snappy v0.0.0-20230731223053-c322873962e3 // indirect
	github.com/eapache/queue v1.1.0 // indirect
	github.com/eapache/queue/v2 v2.0.0-20230407133247-75960ed334e4 // indirect
	github.com/ebitengine/purego v0.8.2 // indirect
	github.com/elastic/elastic-transport-go/v8 v8.1.0 // indirect
	github.com/envoyproxy/protoc-gen-validate v1.2.1 // indirect
	github.com/fatih/color v1.16.0 // indirect
	github.com/felixge/httpsnoop v1.0.4 // indirect
	github.com/fsnotify/fsnotify v1.9.0 // indirect
	github.com/fxamacker/cbor/v2 v2.7.0 // indirect
	github.com/gabriel-vasile/mimetype v1.4.2 // indirect
	github.com/gin-contrib/sse v0.1.0 // indirect
	github.com/go-jose/go-jose/v3 v3.0.3 // indirect
	github.com/go-logr/logr v1.4.2 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/go-ole/go-ole v1.3.0 // indirect
	github.com/go-openapi/jsonpointer v0.21.0 // indirect
	github.com/go-openapi/jsonreference v0.20.2 // indirect
	github.com/go-openapi/swag v0.23.0 // indirect
	github.com/go-pg/zerochecker v0.2.0 // indirect
	github.com/go-playground/locales v0.14.1 // indirect
	github.com/go-playground/universal-translator v0.18.1 // indirect
	github.com/go-playground/validator/v10 v10.15.1 // indirect
	github.com/go-viper/mapstructure/v2 v2.2.1 // indirect
	github.com/goccy/go-json v0.10.2 // indirect
	github.com/goccy/go-yaml v1.17.1 // indirect
	github.com/gofrs/uuid v4.4.0+incompatible // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/golang-sql/civil v0.0.0-20220223132316-b832511892a9 // indirect
	github.com/golang-sql/sqlexp v0.1.0 // indirect
	github.com/golang/groupcache v0.0.0-20210331224755-41bb18bfe9da // indirect
	github.com/golang/protobuf v1.5.4 // indirect
	github.com/golang/snappy v0.0.4 // indirect
	github.com/google/gnostic-models v0.6.8 // indirect
	github.com/google/go-cmp v0.7.0 // indirect
	github.com/google/go-tpm v0.9.3 // indirect
	github.com/google/gofuzz v1.2.0 // indirect
	github.com/google/s2a-go v0.1.7 // indirect
	github.com/googleapis/enterprise-certificate-proxy v0.3.2 // indirect
	github.com/googleapis/gax-go/v2 v2.12.2 // indirect
	github.com/gorilla/websocket v1.5.0 // indirect
	github.com/hailocab/go-hostpool v0.0.0-20160125115350-e80d13ce29ed // indirect
	github.com/hashicorp/errwrap v1.1.0 // indirect
	github.com/hashicorp/go-cleanhttp v0.5.2 // indirect
	github.com/hashicorp/go-hclog v1.6.3 // indirect
	github.com/hashicorp/go-immutable-radix v1.3.1 // indirect
	github.com/hashicorp/go-multierror v1.1.1 // indirect
	github.com/hashicorp/go-retryablehttp v0.7.7 // indirect
	github.com/hashicorp/go-rootcerts v1.0.2 // indirect
	github.com/hashicorp/go-secure-stdlib/parseutil v0.2.0 // indirect
	github.com/hashicorp/go-secure-stdlib/strutil v0.1.2 // indirect
	github.com/hashicorp/go-sockaddr v1.0.7 // indirect
	github.com/hashicorp/go-uuid v1.0.3 // indirect
	github.com/hashicorp/golang-lru v1.0.2 // indirect
	github.com/hashicorp/golang-lru/v2 v2.0.3 // indirect
	github.com/hashicorp/hcl v1.0.1-vault-5 // indirect
	github.com/hashicorp/serf v0.10.1 // indirect
	github.com/hashicorp/vault/sdk v0.9.2 // indirect
	github.com/imdario/mergo v0.3.16 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20221227161230-091c0ba34f0a // indirect
	github.com/jackc/puddle/v2 v2.2.1 // indirect
	github.com/jcmturner/aescts/v2 v2.0.0 // indirect
	github.com/jcmturner/dnsutils/v2 v2.0.0 // indirect
	github.com/jcmturner/gofork v1.7.6 // indirect
	github.com/jcmturner/gokrb5/v8 v8.4.4 // indirect
	github.com/jcmturner/rpc/v2 v2.0.3 // indirect
	github.com/jinzhu/inflection v1.0.0 // indirect
	github.com/jinzhu/now v1.1.5 // indirect
	github.com/jmespath/go-jmespath v0.4.0 // indirect
	github.com/josharian/intern v1.0.0 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/kballard/go-shellquote v0.0.0-20180428030007-95032a82bc51 // indirect
	github.com/klauspost/compress v1.18.0 // indirect
	github.com/klauspost/cpuid/v2 v2.2.5 // indirect
	github.com/labstack/gommon v0.4.2 // indirect
	github.com/leodido/go-urn v1.2.4 // indirect
	github.com/lucasb-eyer/go-colorful v1.2.0 // indirect
	github.com/lufia/plan9stats v0.0.0-20250317134145-8bc96cf8fc35 // indirect
	github.com/mailru/easyjson v0.7.7 // indirect
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/mattn/go-runewidth v0.0.16 // indirect
	github.com/minio/highwayhash v1.0.3 // indirect
	github.com/mitchellh/go-homedir v1.1.0 // indirect
	github.com/mitchellh/mapstructure v1.5.1-0.20231216201459-8508981c8b6c // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/montanaflynn/stats v0.6.6 // indirect
	github.com/muesli/termenv v0.16.0 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/nats-io/jwt/v2 v2.7.3 // indirect
	github.com/nats-io/nats-server/v2 v2.11.1 // indirect
	github.com/nats-io/nats.go v1.41.1 // indirect
	github.com/nats-io/nkeys v0.4.10 // indirect
	github.com/nats-io/nuid v1.0.1 // indirect
	github.com/outcaste-io/ristretto v0.2.3 // indirect
	github.com/pelletier/go-toml/v2 v2.0.9 // indirect
	github.com/philhofer/fwd v1.1.3-0.20240916144458-20a13a1f6b7c // indirect
	github.com/pierrec/lz4/v4 v4.1.18 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/planetscale/vtprotobuf v0.6.1-0.20240319094008-0393e58bdf10 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/power-devops/perfstat v0.0.0-20240221224432-82ca36839d55 // indirect
	github.com/puzpuzpuz/xsync/v3 v3.5.1 // indirect
	github.com/rcrowley/go-metrics v0.0.0-20201227073835-cf1acfcdf475 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/richardartoul/molecule v1.0.1-0.20240531184615-7ca0df43c0b3 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/rs/zerolog v1.34.0 // indirect
	github.com/russross/blackfriday/v2 v2.1.0 // indirect
	github.com/ryanuber/go-glob v1.0.0 // indirect
	github.com/secure-systems-lab/go-securesystemslib v0.9.0 // indirect
	github.com/shirou/gopsutil/v4 v4.25.3 // indirect
	github.com/spaolacci/murmur3 v1.1.0 // indirect
	github.com/spf13/pflag v1.0.5 // indirect
	github.com/tidwall/btree v1.6.0 // indirect
	github.com/tidwall/gjson v1.16.0 // indirect
	github.com/tidwall/grect v0.1.4 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/tidwall/rtred v0.1.2 // indirect
	github.com/tidwall/tinyqueue v0.1.1 // indirect
	github.com/tklauser/go-sysconf v0.3.15 // indirect
	github.com/tklauser/numcpus v0.10.0 // indirect
	github.com/tmthrgd/go-hex v0.0.0-20190904060850-447a3041c3bc // indirect
	github.com/twitchyliquid64/golang-asm v0.15.1 // indirect
	github.com/ugorji/go/codec v1.2.11 // indirect
	github.com/urfave/cli/v2 v2.27.6 // indirect
	github.com/valyala/bytebufferpool v1.0.0 // indirect
	github.com/valyala/fasttemplate v1.2.2 // indirect
	github.com/valyala/tcplisten v1.0.0 // indirect
	github.com/vmihailenco/bufpool v0.1.11 // indirect
	github.com/vmihailenco/msgpack/v5 v5.4.1 // indirect
	github.com/vmihailenco/tagparser v0.1.2 // indirect
	github.com/vmihailenco/tagparser/v2 v2.0.0 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	github.com/xdg-go/pbkdf2 v1.0.0 // indirect
	github.com/xdg-go/scram v1.1.2 // indirect
	github.com/xdg-go/stringprep v1.0.4 // indirect
	github.com/xeipuuv/gojsonpointer v0.0.0-20190905194746-02993c407bfb // indirect
	github.com/xeipuuv/gojsonreference v0.0.0-20180127040603-bd5ef7bd5415 // indirect
	github.com/xeipuuv/gojsonschema v1.2.0 // indirect
	github.com/xo/terminfo v0.0.0-20220910002029-abceb7e1c41e // indirect
	github.com/xrash/smetrics v0.0.0-20240521201337-686a1a2994c1 // indirect
	github.com/youmark/pkcs8 v0.0.0-20181117223130-1be2e3e5546d // indirect
	github.com/yusufpapurcu/wmi v1.2.4 // indirect
	go.einride.tech/aip v0.66.0 // indirect
	go.opencensus.io v0.24.0 // indirect
	go.opentelemetry.io/auto/sdk v1.1.0 // indirect
	go.opentelemetry.io/collector/component v1.28.1 // indirect
	go.opentelemetry.io/collector/pdata v1.29.0 // indirect
	go.opentelemetry.io/collector/semconv v0.123.0 // indirect
	go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc v0.49.0 // indirect
	go.opentelemetry.io/otel/metric v1.35.0 // indirect
	go.opentelemetry.io/otel/sdk v1.34.0 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.uber.org/zap v1.27.0 // indirect
	golang.org/x/arch v0.4.0 // indirect
	golang.org/x/crypto v0.37.0 // indirect
	golang.org/x/exp v0.0.0-20250210185358-939b2ce775ac // indirect
	golang.org/x/net v0.39.0 // indirect
	golang.org/x/term v0.31.0 // indirect
	golang.org/x/text v0.24.0 // indirect
	google.golang.org/genproto v0.0.0-20240325203815-454cdb8f5daa // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20250115164207-1a7da9e5054f // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20250409194420-de1ac958c67a // indirect
	gopkg.in/inf.v0 v0.9.1 // indirect
	gopkg.in/ini.v1 v1.67.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	k8s.io/api v0.31.4 // indirect
	k8s.io/klog/v2 v2.130.1 // indirect
	k8s.io/kube-openapi v0.0.0-20241105132330-32ad38e42d3f // indirect
	k8s.io/utils v0.0.0-20241210054802-24370beab758 // indirect
	lukechampine.com/uint128 v1.3.0 // indirect
	mellium.im/sasl v0.3.1 // indirect
	modernc.org/cc/v3 v3.41.0 // indirect
	modernc.org/ccgo/v3 v3.16.15 // indirect
	modernc.org/libc v1.37.6 // indirect
	modernc.org/mathutil v1.6.0 // indirect
	modernc.org/memory v1.7.2 // indirect
	modernc.org/opt v0.1.3 // indirect
	modernc.org/strutil v1.2.0 // indirect
	modernc.org/token v1.1.0 // indirect
	sigs.k8s.io/json v0.0.0-20241010143419-9aa6b5e7a4b3 // indirect
	sigs.k8s.io/structured-merge-diff/v4 v4.4.2 // indirect
	sigs.k8s.io/yaml v1.4.0 // indirect
)

// Retract experimental versions
retract (
	v1.999.0-rc.8
	v1.999.0-rc.7
	v1.999.0-rc.6
	v1.999.0-rc.5
	v1.999.0-rc.4
	v1.999.0-rc.3
	v1.999.0-rc.2
	v1.999.0-rc.1
	v1.999.0-beta.13
	v1.999.0-beta.12
)
