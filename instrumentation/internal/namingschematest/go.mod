module github.com/DataDog/dd-trace-go/instrumentation/internal/namingschematest/v2

go 1.21

require (
	cloud.google.com/go/pubsub v1.41.0
	github.com/99designs/gqlgen v0.17.49
	github.com/DataDog/dd-trace-go/contrib/99designs/gqlgen/v2 v2.0.0-20240909105439-c452671ebc14
	github.com/DataDog/dd-trace-go/contrib/aws/aws-sdk-go-v2/v2 v2.0.0-20240909105439-c452671ebc14
	github.com/DataDog/dd-trace-go/contrib/aws/aws-sdk-go/v2 v2.0.0-20240909105439-c452671ebc14
	github.com/DataDog/dd-trace-go/contrib/bradfitz/gomemcache/v2 v2.0.0-20240909105439-c452671ebc14
	github.com/DataDog/dd-trace-go/contrib/cloud.google.com/go/pubsub.v1/v2 v2.0.0-20240909105439-c452671ebc14
	github.com/DataDog/dd-trace-go/contrib/confluentinc/confluent-kafka-go/kafka.v2/v2 v2.0.0-20240909105439-c452671ebc14
	github.com/DataDog/dd-trace-go/contrib/confluentinc/confluent-kafka-go/kafka/v2 v2.0.0-20240909105439-c452671ebc14
	github.com/DataDog/dd-trace-go/contrib/database/sql/v2 v2.0.0-20240909105439-c452671ebc14
	github.com/DataDog/dd-trace-go/contrib/dimfeld/httptreemux.v5/v2 v2.0.0-20240909105439-c452671ebc14
	github.com/DataDog/dd-trace-go/contrib/elastic/go-elasticsearch.v6/v2 v2.0.0-20240909105439-c452671ebc14
	github.com/DataDog/dd-trace-go/contrib/emicklei/go-restful.v3/v2 v2.0.0-20240909105439-c452671ebc14
	github.com/DataDog/dd-trace-go/contrib/gin-gonic/gin/v2 v2.0.0-20240909105439-c452671ebc14
	github.com/DataDog/dd-trace-go/contrib/globalsign/mgo/v2 v2.0.0-20240909105439-c452671ebc14
	github.com/DataDog/dd-trace-go/contrib/go-chi/chi.v5/v2 v2.0.0-20240909105439-c452671ebc14
	github.com/DataDog/dd-trace-go/contrib/go-chi/chi/v2 v2.0.0-20240909105439-c452671ebc14
	github.com/DataDog/dd-trace-go/contrib/go-pg/pg.v10/v2 v2.0.0-20240909105439-c452671ebc14
	github.com/DataDog/dd-trace-go/contrib/go-redis/redis.v7/v2 v2.0.0-20240909105439-c452671ebc14
	github.com/DataDog/dd-trace-go/contrib/go-redis/redis.v8/v2 v2.0.0-20240909105439-c452671ebc14
	github.com/DataDog/dd-trace-go/contrib/go-redis/redis/v2 v2.0.0-20240909105439-c452671ebc14
	github.com/DataDog/dd-trace-go/contrib/go.mongodb.org/mongo-driver/v2 v2.0.0-20240909105439-c452671ebc14
	github.com/DataDog/dd-trace-go/contrib/gocql/gocql/v2 v2.0.0-20240909105439-c452671ebc14
	github.com/DataDog/dd-trace-go/contrib/gofiber/fiber.v2/v2 v2.0.0-20240909105439-c452671ebc14
	github.com/DataDog/dd-trace-go/contrib/gomodule/redigo/v2 v2.0.0-20240827110213-c6fc4fe2047a
	github.com/DataDog/dd-trace-go/contrib/google.golang.org/grpc/v2 v2.0.0-beta.2.0.20240909105439-c452671ebc14
	github.com/DataDog/dd-trace-go/contrib/gorilla/mux/v2 v2.0.0-20240909105439-c452671ebc14
	github.com/DataDog/dd-trace-go/contrib/graph-gophers/graphql-go/v2 v2.0.0-20240909105439-c452671ebc14
	github.com/DataDog/dd-trace-go/contrib/hashicorp/consul/v2 v2.0.0-20240909105439-c452671ebc14
	github.com/DataDog/dd-trace-go/contrib/hashicorp/vault/v2 v2.0.0-20240909105439-c452671ebc14
	github.com/DataDog/dd-trace-go/contrib/julienschmidt/httprouter/v2 v2.0.0-20240909105439-c452671ebc14
	github.com/DataDog/dd-trace-go/contrib/labstack/echo.v4/v2 v2.0.0-20240909105439-c452671ebc14
	github.com/DataDog/dd-trace-go/contrib/net/http/v2 v2.0.0-20240909105439-c452671ebc14
	github.com/DataDog/dd-trace-go/contrib/olivere/elastic.v5/v2 v2.0.0-20240909105439-c452671ebc14
	github.com/DataDog/dd-trace-go/contrib/redis/go-redis.v9/v2 v2.0.0-20240909105439-c452671ebc14
	github.com/DataDog/dd-trace-go/contrib/segmentio/kafka-go/v2 v2.0.0-20240909105439-c452671ebc14
	github.com/DataDog/dd-trace-go/contrib/syndtr/goleveldb/v2 v2.0.0-20240909105439-c452671ebc14
	github.com/DataDog/dd-trace-go/contrib/tidwall/buntdb/v2 v2.0.0-20240909105439-c452671ebc14
	github.com/DataDog/dd-trace-go/contrib/twitchtv/twirp/v2 v2.0.0-20240909105439-c452671ebc14
	github.com/DataDog/dd-trace-go/contrib/urfave/negroni/v2 v2.0.0-20240909105439-c452671ebc14
	github.com/DataDog/dd-trace-go/instrumentation/testutils/grpc/v2 v2.0.0-20240827110213-c6fc4fe2047a
	github.com/DataDog/dd-trace-go/v2 v2.0.0-20240909105439-c452671ebc14
	github.com/IBM/sarama v1.40.0
	github.com/Shopify/sarama v1.38.1
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
	github.com/gin-gonic/gin v1.10.0
	github.com/globalsign/mgo v0.0.0-20181015135952-eeefdecb41b8
	github.com/go-chi/chi v1.5.5
	github.com/go-chi/chi/v5 v5.1.0
	github.com/go-pg/pg/v10 v10.13.0
	github.com/go-redis/redis v6.15.9+incompatible
	github.com/go-redis/redis/v7 v7.4.1
	github.com/go-redis/redis/v8 v8.11.5
	github.com/go-sql-driver/mysql v1.8.1
	github.com/gocql/gocql v1.6.0
	github.com/gofiber/fiber/v2 v2.52.5
	github.com/graph-gophers/graphql-go v1.5.0
	github.com/hashicorp/consul/api v1.29.4
	github.com/hashicorp/vault/api v1.12.1
	github.com/julienschmidt/httprouter v1.3.0
	github.com/labstack/echo/v4 v4.12.0
	github.com/lib/pq v1.10.9
	github.com/redis/go-redis/v9 v9.6.1
	github.com/segmentio/kafka-go v0.4.47
	github.com/stretchr/testify v1.9.0
	github.com/syndtr/goleveldb v1.0.1-0.20220721030215-126854af5e6d
	github.com/twitchtv/twirp v8.1.3+incompatible
	github.com/urfave/negroni v1.0.0
	go.mongodb.org/mongo-driver v1.16.1
	google.golang.org/api v0.191.0
	google.golang.org/grpc v1.65.0
	gopkg.in/olivere/elastic.v5 v5.0.86
)

require (
	github.com/DataDog/go-libddwaf/v3 v3.3.0 // indirect
	github.com/armon/go-metrics v0.4.1 // indirect
	github.com/cenkalti/backoff/v3 v3.2.2 // indirect
	github.com/eapache/go-resiliency v1.4.0 // indirect
	github.com/eapache/go-xerial-snappy v0.0.0-20230731223053-c322873962e3 // indirect
	github.com/eapache/queue v1.1.0 // indirect
	github.com/eapache/queue/v2 v2.0.0-20230407133247-75960ed334e4 // indirect
	github.com/fatih/color v1.16.0 // indirect
	github.com/go-jose/go-jose/v3 v3.0.3 // indirect
	github.com/gorilla/mux v1.8.1 // indirect
	github.com/hashicorp/go-cleanhttp v0.5.2 // indirect
	github.com/hashicorp/go-hclog v1.5.0 // indirect
	github.com/hashicorp/go-immutable-radix v1.3.1 // indirect
	github.com/hashicorp/go-retryablehttp v0.7.5 // indirect
	github.com/hashicorp/go-rootcerts v1.0.2 // indirect
	github.com/hashicorp/go-secure-stdlib/parseutil v0.1.8 // indirect
	github.com/hashicorp/go-secure-stdlib/strutil v0.1.2 // indirect
	github.com/hashicorp/go-sockaddr v1.0.5 // indirect
	github.com/hashicorp/go-uuid v1.0.3 // indirect
	github.com/hashicorp/golang-lru v1.0.2 // indirect
	github.com/hashicorp/hcl v1.0.1-vault-5 // indirect
	github.com/hashicorp/serf v0.10.1 // indirect
	github.com/hashicorp/vault/sdk v0.9.2 // indirect
	github.com/jcmturner/aescts/v2 v2.0.0 // indirect
	github.com/jcmturner/dnsutils/v2 v2.0.0 // indirect
	github.com/jcmturner/gofork v1.7.6 // indirect
	github.com/jcmturner/gokrb5/v8 v8.4.4 // indirect
	github.com/jcmturner/rpc/v2 v2.0.3 // indirect
	github.com/josharian/intern v1.0.0 // indirect
	github.com/labstack/gommon v0.4.2 // indirect
	github.com/mailru/easyjson v0.7.7 // indirect
	github.com/mitchellh/go-homedir v1.1.0 // indirect
	github.com/pierrec/lz4/v4 v4.1.18 // indirect
	github.com/rcrowley/go-metrics v0.0.0-20201227073835-cf1acfcdf475 // indirect
	github.com/ryanuber/go-glob v1.0.0 // indirect
	github.com/tidwall/btree v1.6.0 // indirect
	github.com/tidwall/buntdb v1.3.0 // indirect
	github.com/tidwall/gjson v1.16.0 // indirect
	github.com/tidwall/grect v0.1.4 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/tidwall/rtred v0.1.2 // indirect
	github.com/tidwall/tinyqueue v0.1.1 // indirect
	github.com/valyala/fasttemplate v1.2.2 // indirect
	golang.org/x/exp v0.0.0-20240112132812-db7319d0e0e3 // indirect
	golang.org/x/mod v0.18.0 // indirect
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
	github.com/DataDog/dd-trace-go/contrib/IBM/sarama/v2 v2.0.0-20240909105439-c452671ebc14
	github.com/DataDog/dd-trace-go/contrib/Shopify/sarama/v2 v2.0.0-20240909105439-c452671ebc14
	github.com/DataDog/go-sqllexer v0.0.12 // indirect
	github.com/DataDog/go-tuf v1.1.0-0.5.2 // indirect
	github.com/DataDog/sketches-go v1.4.6 // indirect
	github.com/Microsoft/go-winio v0.6.2 // indirect
	github.com/agnivade/levenshtein v1.1.1 // indirect
	github.com/andybalholm/brotli v1.0.6 // indirect
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
	github.com/bytedance/sonic v1.11.6 // indirect
	github.com/bytedance/sonic/loader v0.1.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/cloudwego/base64x v0.1.4 // indirect
	github.com/cloudwego/iasm v0.2.0 // indirect
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/dimfeld/httptreemux/v5 v5.5.0 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/ebitengine/purego v0.7.1 // indirect
	github.com/elastic/elastic-transport-go/v8 v8.6.0 // indirect
	github.com/felixge/httpsnoop v1.0.4 // indirect
	github.com/gabriel-vasile/mimetype v1.4.3 // indirect
	github.com/gin-contrib/sse v0.1.0 // indirect
	github.com/go-logr/logr v1.4.2 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/go-pg/zerochecker v0.2.0 // indirect
	github.com/go-playground/locales v0.14.1 // indirect
	github.com/go-playground/universal-translator v0.18.1 // indirect
	github.com/go-playground/validator/v10 v10.20.0 // indirect
	github.com/goccy/go-json v0.10.2 // indirect
	github.com/golang-sql/civil v0.0.0-20220223132316-b832511892a9 // indirect
	github.com/golang-sql/sqlexp v0.1.0 // indirect
	github.com/golang/groupcache v0.0.0-20210331224755-41bb18bfe9da // indirect
	github.com/golang/snappy v0.0.4 // indirect
	github.com/gomodule/redigo v1.8.9 // indirect
	github.com/google/go-cmp v0.6.0 // indirect
	github.com/google/s2a-go v0.1.8 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/googleapis/enterprise-certificate-proxy v0.3.2 // indirect
	github.com/googleapis/gax-go/v2 v2.13.0 // indirect
	github.com/gorilla/websocket v1.5.0 // indirect
	github.com/hailocab/go-hostpool v0.0.0-20160125115350-e80d13ce29ed // indirect
	github.com/hashicorp/errwrap v1.1.0 // indirect
	github.com/hashicorp/go-multierror v1.1.1 // indirect
	github.com/hashicorp/golang-lru/v2 v2.0.7 // indirect
	github.com/jinzhu/inflection v1.0.0 // indirect
	github.com/jmespath/go-jmespath v0.4.0 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/klauspost/compress v1.17.7 // indirect
	github.com/klauspost/cpuid/v2 v2.2.7 // indirect
	github.com/leodido/go-urn v1.4.0 // indirect
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/mattn/go-runewidth v0.0.15 // indirect
	github.com/mitchellh/mapstructure v1.5.0 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/montanaflynn/stats v0.7.1 // indirect
	github.com/outcaste-io/ristretto v0.2.3 // indirect
	github.com/pelletier/go-toml/v2 v2.2.2 // indirect
	github.com/philhofer/fwd v1.1.3-0.20240612014219-fbbf4953d986 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/rivo/uniseg v0.4.4 // indirect
	github.com/secure-systems-lab/go-securesystemslib v0.8.0 // indirect
	github.com/sosodev/duration v1.3.1 // indirect
	github.com/tinylib/msgp v1.2.0 // indirect
	github.com/tmthrgd/go-hex v0.0.0-20190904060850-447a3041c3bc // indirect
	github.com/twitchyliquid64/golang-asm v0.15.1 // indirect
	github.com/ugorji/go/codec v1.2.12 // indirect
	github.com/valyala/bytebufferpool v1.0.0 // indirect
	github.com/valyala/fasthttp v1.51.0 // indirect
	github.com/valyala/tcplisten v1.0.0 // indirect
	github.com/vektah/gqlparser/v2 v2.5.16 // indirect
	github.com/vmihailenco/bufpool v0.1.11 // indirect
	github.com/vmihailenco/msgpack/v5 v5.3.5 // indirect
	github.com/vmihailenco/tagparser v0.1.2 // indirect
	github.com/vmihailenco/tagparser/v2 v2.0.0 // indirect
	github.com/xdg-go/pbkdf2 v1.0.0 // indirect
	github.com/xdg-go/scram v1.1.2 // indirect
	github.com/xdg-go/stringprep v1.0.4 // indirect
	github.com/youmark/pkcs8 v0.0.0-20181117223130-1be2e3e5546d // indirect
	go.einride.tech/aip v0.67.1 // indirect
	go.opencensus.io v0.24.0 // indirect
	go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc v0.49.0 // indirect
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.49.0 // indirect
	go.opentelemetry.io/otel v1.24.0 // indirect
	go.opentelemetry.io/otel/metric v1.24.0 // indirect
	go.opentelemetry.io/otel/sdk v1.24.0 // indirect
	go.opentelemetry.io/otel/trace v1.24.0 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	golang.org/x/arch v0.8.0 // indirect
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
	gopkg.in/inf.v0 v0.9.1 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	mellium.im/sasl v0.3.1 // indirect
)

replace github.com/DataDog/dd-trace-go/contrib/99designs/gqlgen/v2 => ../../../contrib/99designs/gqlgen

replace github.com/DataDog/dd-trace-go/contrib/IBM/sarama/v2 => ../../../contrib/IBM/sarama

replace github.com/DataDog/dd-trace-go/contrib/Shopify/sarama/v2 => ../../../contrib/Shopify/sarama

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

replace github.com/DataDog/dd-trace-go/contrib/gin-gonic/gin/v2 => ../../../contrib/gin-gonic/gin

replace github.com/DataDog/dd-trace-go/contrib/globalsign/mgo/v2 => ../../../contrib/globalsign/mgo

replace github.com/DataDog/dd-trace-go/contrib/go-chi/chi.v5/v2 => ../../../contrib/go-chi/chi.v5

replace github.com/DataDog/dd-trace-go/contrib/go-chi/chi/v2 => ../../../contrib/go-chi/chi

replace github.com/DataDog/dd-trace-go/contrib/go-pg/pg.v10/v2 => ../../../contrib/go-pg/pg.v10

replace github.com/DataDog/dd-trace-go/contrib/go-redis/redis.v7/v2 => ../../../contrib/go-redis/redis.v7

replace github.com/DataDog/dd-trace-go/contrib/go-redis/redis.v8/v2 => ../../../contrib/go-redis/redis.v8

replace github.com/DataDog/dd-trace-go/contrib/go-redis/redis/v2 => ../../../contrib/go-redis/redis

replace github.com/DataDog/dd-trace-go/contrib/go.mongodb.org/mongo-driver/v2 => ../../../contrib/go.mongodb.org/mongo-driver

replace github.com/DataDog/dd-trace-go/contrib/gocql/gocql/v2 => ../../../contrib/gocql/gocql

replace github.com/DataDog/dd-trace-go/contrib/gofiber/fiber.v2/v2 => ../../../contrib/gofiber/fiber.v2

replace github.com/DataDog/dd-trace-go/contrib/gomodule/redigo/v2 => ../../../contrib/gomodule/redigo

replace github.com/DataDog/dd-trace-go/contrib/google.golang.org/grpc/v2 => ../../../contrib/google.golang.org/grpc

replace github.com/DataDog/dd-trace-go/contrib/gorilla/mux/v2 => ../../../contrib/gorilla/mux

replace github.com/DataDog/dd-trace-go/contrib/graph-gophers/graphql-go/v2 => ../../../contrib/graph-gophers/graphql-go

replace github.com/DataDog/dd-trace-go/contrib/hashicorp/consul/v2 => ../../../contrib/hashicorp/consul

replace github.com/DataDog/dd-trace-go/contrib/hashicorp/vault/v2 => ../../../contrib/hashicorp/vault

replace github.com/DataDog/dd-trace-go/contrib/julienschmidt/httprouter/v2 => ../../../contrib/julienschmidt/httprouter

replace github.com/DataDog/dd-trace-go/contrib/labstack/echo.v4/v2 => ../../../contrib/labstack/echo.v4

replace github.com/DataDog/dd-trace-go/contrib/net/http/v2 => ../../../contrib/net/http

replace github.com/DataDog/dd-trace-go/contrib/olivere/elastic.v5/v2 => ../../../contrib/olivere/elastic.v5

replace github.com/DataDog/dd-trace-go/contrib/redis/go-redis.v9/v2 => ../../../contrib/redis/go-redis.v9

replace github.com/DataDog/dd-trace-go/contrib/segmentio/kafka-go/v2 => ../../../contrib/segmentio/kafka-go

replace github.com/DataDog/dd-trace-go/contrib/syndtr/goleveldb/v2 => ../../../contrib/syndtr/goleveldb

replace github.com/DataDog/dd-trace-go/contrib/tidwall/buntdb/v2 => ../../../contrib/tidwall/buntdb

replace github.com/DataDog/dd-trace-go/contrib/twitchtv/twirp/v2 => ../../../contrib/twitchtv/twirp

replace github.com/DataDog/dd-trace-go/contrib/urfave/negroni/v2 => ../../../contrib/urfave/negroni

replace github.com/DataDog/dd-trace-go/instrumentation/testutils/grpc/v2 => ../../testutils/grpc

replace github.com/DataDog/dd-trace-go/v2 => ../../..