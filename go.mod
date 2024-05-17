module gopkg.in/DataDog/dd-trace-go.v1

go 1.21

toolchain go1.21.5

require (
	cloud.google.com/go/pubsub v1.33.0
	github.com/99designs/gqlgen v0.17.36
	github.com/DataDog/appsec-internal-go v1.5.0
	github.com/DataDog/dd-trace-go/v2 v2.0.0-20240320152540-7ebff8f16ee3
	github.com/DataDog/dd-trace-go/v2/contrib/99designs/gqlgen v0.0.0-20240320152540-7ebff8f16ee3
	github.com/DataDog/dd-trace-go/v2/contrib/IBM/sarama v0.0.0-20240320152540-7ebff8f16ee3
	github.com/DataDog/dd-trace-go/v2/contrib/Shopify/sarama v0.0.0-20240320152540-7ebff8f16ee3
	github.com/DataDog/dd-trace-go/v2/contrib/aws/aws-sdk-go v0.0.0-20240320152540-7ebff8f16ee3
	github.com/DataDog/dd-trace-go/v2/contrib/aws/aws-sdk-go-v2 v0.0.0-20240320152540-7ebff8f16ee3
	github.com/DataDog/dd-trace-go/v2/contrib/bradfitz/gomemcache v0.0.0-20240320152540-7ebff8f16ee3
	github.com/DataDog/dd-trace-go/v2/contrib/cloud.google.com/go/pubsub.v1 v0.0.0-20240320152540-7ebff8f16ee3
	github.com/DataDog/dd-trace-go/v2/contrib/confluentinc/confluent-kafka-go/kafka v0.0.0-20240320152540-7ebff8f16ee3
	github.com/DataDog/dd-trace-go/v2/contrib/confluentinc/confluent-kafka-go/kafka.v2 v0.0.0-20240320152540-7ebff8f16ee3
	github.com/DataDog/dd-trace-go/v2/contrib/database/sql v0.0.0-20240320152540-7ebff8f16ee3
	github.com/DataDog/dd-trace-go/v2/contrib/dimfeld/httptreemux.v5 v0.0.0-20240320152540-7ebff8f16ee3
	github.com/DataDog/dd-trace-go/v2/contrib/elastic/go-elasticsearch.v6 v0.0.0-20240320152540-7ebff8f16ee3
	github.com/DataDog/dd-trace-go/v2/contrib/emicklei/go-restful.v3 v0.0.0-20240320152540-7ebff8f16ee3
	github.com/DataDog/dd-trace-go/v2/contrib/gin-gonic/gin v0.0.0-20240320152540-7ebff8f16ee3
	github.com/DataDog/dd-trace-go/v2/contrib/globalsign/mgo v0.0.0-20240320152540-7ebff8f16ee3
	github.com/DataDog/dd-trace-go/v2/contrib/go-chi/chi v0.0.0-20240320152540-7ebff8f16ee3
	github.com/DataDog/dd-trace-go/v2/contrib/go-chi/chi.v5 v0.0.0-20240320152540-7ebff8f16ee3
	github.com/DataDog/dd-trace-go/v2/contrib/go-pg/pg.v10 v0.0.0-20240320152540-7ebff8f16ee3
	github.com/DataDog/dd-trace-go/v2/contrib/go-redis/redis v0.0.0-20240320152540-7ebff8f16ee3
	github.com/DataDog/dd-trace-go/v2/contrib/go-redis/redis.v7 v0.0.0-20240320152540-7ebff8f16ee3
	github.com/DataDog/dd-trace-go/v2/contrib/go-redis/redis.v8 v0.0.0-20240320152540-7ebff8f16ee3
	github.com/DataDog/dd-trace-go/v2/contrib/go.mongodb.org/mongo-driver v0.0.0-20240320152540-7ebff8f16ee3
	github.com/DataDog/dd-trace-go/v2/contrib/gocql/gocql v0.0.0-20240320152540-7ebff8f16ee3
	github.com/DataDog/dd-trace-go/v2/contrib/gofiber/fiber.v2 v0.0.0-20240320152540-7ebff8f16ee3
	github.com/DataDog/dd-trace-go/v2/contrib/gomodule/redigo v0.0.0-20240308141714-cc13161300f3
	github.com/DataDog/dd-trace-go/v2/contrib/google.golang.org/api v0.0.0-20240320152540-7ebff8f16ee3
	github.com/DataDog/dd-trace-go/v2/contrib/google.golang.org/grpc v0.0.0-20240320152540-7ebff8f16ee3
	github.com/DataDog/dd-trace-go/v2/contrib/gorilla/mux v0.0.0-20240320152540-7ebff8f16ee3
	github.com/DataDog/dd-trace-go/v2/contrib/gorm.io/gorm.v1 v0.0.0-20240320152540-7ebff8f16ee3
	github.com/DataDog/dd-trace-go/v2/contrib/graph-gophers/graphql-go v0.0.0-20240320152540-7ebff8f16ee3
	github.com/DataDog/dd-trace-go/v2/contrib/graphql-go/graphql v0.0.0-20240320152540-7ebff8f16ee3
	github.com/DataDog/dd-trace-go/v2/contrib/hashicorp/consul v0.0.0-20240320152540-7ebff8f16ee3
	github.com/DataDog/dd-trace-go/v2/contrib/hashicorp/vault v0.0.0-20240320152540-7ebff8f16ee3
	github.com/DataDog/dd-trace-go/v2/contrib/jackc/pgx.v5 v0.0.0-20240320152540-7ebff8f16ee3
	github.com/DataDog/dd-trace-go/v2/contrib/jmoiron/sqlx v0.0.0-20240320152540-7ebff8f16ee3
	github.com/DataDog/dd-trace-go/v2/contrib/julienschmidt/httprouter v0.0.0-20240320152540-7ebff8f16ee3
	github.com/DataDog/dd-trace-go/v2/contrib/k8s.io/client-go v0.0.0-20240320152540-7ebff8f16ee3
	github.com/DataDog/dd-trace-go/v2/contrib/labstack/echo.v4 v0.0.0-20240320152540-7ebff8f16ee3
	github.com/DataDog/dd-trace-go/v2/contrib/miekg/dns v0.0.0-20240320152540-7ebff8f16ee3
	github.com/DataDog/dd-trace-go/v2/contrib/net/http v0.0.0-20240320152540-7ebff8f16ee3
	github.com/DataDog/dd-trace-go/v2/contrib/olivere/elastic.v5 v0.0.0-20240320152540-7ebff8f16ee3
	github.com/DataDog/dd-trace-go/v2/contrib/redis/go-redis.v9 v0.0.0-20240320152540-7ebff8f16ee3
	github.com/DataDog/dd-trace-go/v2/contrib/segmentio/kafka-go v0.0.0-20240320152540-7ebff8f16ee3
	github.com/DataDog/dd-trace-go/v2/contrib/sirupsen/logrus v0.0.0-20240320152540-7ebff8f16ee3
	github.com/DataDog/dd-trace-go/v2/contrib/syndtr/goleveldb v0.0.0-20240320152540-7ebff8f16ee3
	github.com/DataDog/dd-trace-go/v2/contrib/tidwall/buntdb v0.0.0-20240320152540-7ebff8f16ee3
	github.com/DataDog/dd-trace-go/v2/contrib/twitchtv/twirp v0.0.0-20240320152540-7ebff8f16ee3
	github.com/DataDog/dd-trace-go/v2/contrib/urfave/negroni v0.0.0-20240320152540-7ebff8f16ee3
	github.com/DataDog/dd-trace-go/v2/contrib/valyala/fasthttp v0.0.0-20240320152540-7ebff8f16ee3
	github.com/DataDog/gostackparse v0.7.0
	github.com/IBM/sarama v1.40.0
	github.com/Shopify/sarama v1.38.1
	github.com/aws/aws-sdk-go v1.44.327
	github.com/aws/aws-sdk-go-v2 v1.21.2
	github.com/aws/aws-sdk-go-v2/config v1.19.0
	github.com/aws/aws-sdk-go-v2/service/dynamodb v1.21.4
	github.com/aws/aws-sdk-go-v2/service/ec2 v1.93.2
	github.com/aws/aws-sdk-go-v2/service/eventbridge v1.20.4
	github.com/aws/aws-sdk-go-v2/service/kinesis v1.18.4
	github.com/aws/aws-sdk-go-v2/service/s3 v1.32.0
	github.com/aws/aws-sdk-go-v2/service/sfn v1.19.4
	github.com/aws/aws-sdk-go-v2/service/sns v1.21.4
	github.com/aws/aws-sdk-go-v2/service/sqs v1.24.4
	github.com/bradfitz/gomemcache v0.0.0-20230611145640-acc696258285
	github.com/confluentinc/confluent-kafka-go v1.9.2
	github.com/confluentinc/confluent-kafka-go/v2 v2.2.0
	github.com/denisenkom/go-mssqldb v0.12.3
	github.com/dimfeld/httptreemux/v5 v5.5.0
	github.com/elastic/go-elasticsearch/v6 v6.8.5
	github.com/elastic/go-elasticsearch/v7 v7.17.1
	github.com/elastic/go-elasticsearch/v8 v8.4.0
	github.com/emicklei/go-restful v2.16.0+incompatible
	github.com/emicklei/go-restful/v3 v3.11.0
	github.com/garyburd/redigo v1.6.4
	github.com/gin-gonic/gin v1.9.1
	github.com/globalsign/mgo v0.0.0-20181015135952-eeefdecb41b8
	github.com/go-chi/chi v1.5.4
	github.com/go-chi/chi/v5 v5.0.10
	github.com/go-pg/pg/v10 v10.11.1
	github.com/go-redis/redis v6.15.9+incompatible
	github.com/go-redis/redis/v7 v7.4.1
	github.com/go-redis/redis/v8 v8.11.5
	github.com/go-sql-driver/mysql v1.7.1
	github.com/gocql/gocql v0.0.0-20220224095938-0eacd3183625
	github.com/gofiber/fiber/v2 v2.52.1
	github.com/golang/protobuf v1.5.3
	github.com/gomodule/redigo v1.8.9
	github.com/google/pprof v0.0.0-20230817174616-7a8ec2ada47b
	github.com/google/uuid v1.5.0
	github.com/gorilla/mux v1.8.0
	github.com/graph-gophers/graphql-go v1.5.0
	github.com/graphql-go/graphql v0.8.1
	github.com/graphql-go/handler v0.2.3
	github.com/hashicorp/consul/api v1.24.0
	github.com/hashicorp/go-secure-stdlib/parseutil v0.1.7
	github.com/hashicorp/vault/api v1.9.2
	github.com/jackc/pgx/v5 v5.4.2
	github.com/jinzhu/gorm v1.9.16
	github.com/jmoiron/sqlx v1.3.5
	github.com/julienschmidt/httprouter v1.3.0
	github.com/labstack/echo v3.3.10+incompatible
	github.com/labstack/echo/v4 v4.11.1
	github.com/lib/pq v1.10.9
	github.com/mattn/go-sqlite3 v1.14.16
	github.com/microsoft/go-mssqldb v0.21.0
	github.com/miekg/dns v1.1.57
	github.com/opentracing/opentracing-go v1.2.0
	github.com/redis/go-redis/v9 v9.1.0
	github.com/segmentio/kafka-go v0.4.42
	github.com/sirupsen/logrus v1.9.3
	github.com/stretchr/testify v1.8.4
	github.com/syndtr/goleveldb v1.0.1-0.20220721030215-126854af5e6d
	github.com/tidwall/buntdb v1.3.0
	github.com/tinylib/msgp v1.1.9
	github.com/twitchtv/twirp v8.1.3+incompatible
	github.com/urfave/negroni v1.0.0
	github.com/valyala/fasthttp v1.51.0
	github.com/zenazn/goji v1.0.1
	go.mongodb.org/mongo-driver v1.12.1
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.44.0
	go.opentelemetry.io/otel v1.20.0
	go.opentelemetry.io/otel/trace v1.20.0
	golang.org/x/net v0.23.0
	golang.org/x/time v0.5.0
	google.golang.org/api v0.149.0
	google.golang.org/grpc v1.60.1
	google.golang.org/protobuf v1.31.0
	gopkg.in/jinzhu/gorm.v1 v1.9.2
	gopkg.in/olivere/elastic.v3 v3.0.75
	gopkg.in/olivere/elastic.v5 v5.0.84
	gorm.io/driver/mysql v1.0.1
	gorm.io/driver/postgres v1.4.6
	gorm.io/driver/sqlserver v1.4.2
	gorm.io/gorm v1.25.3
	honnef.co/go/gotraceui v0.2.0
	k8s.io/apimachinery v0.23.17
	k8s.io/client-go v0.23.17
)

require (
	cloud.google.com/go v0.110.10 // indirect
	cloud.google.com/go/compute v1.23.3 // indirect
	cloud.google.com/go/compute/metadata v0.2.3 // indirect
	cloud.google.com/go/iam v1.1.5 // indirect
	github.com/DataDog/datadog-agent/pkg/obfuscate v0.50.0 // indirect
	github.com/DataDog/datadog-agent/pkg/remoteconfig/state v0.50.0 // indirect
	github.com/DataDog/datadog-go/v5 v5.4.0 // indirect
	github.com/DataDog/go-libddwaf/v2 v2.3.2 // indirect
	github.com/DataDog/go-sqllexer v0.0.10 // indirect
	github.com/DataDog/go-tuf v1.0.2-0.5.2 // indirect
	github.com/DataDog/sketches-go v1.4.3 // indirect
	github.com/Microsoft/go-winio v0.6.1 // indirect
	github.com/agnivade/levenshtein v1.1.1 // indirect
	github.com/andybalholm/brotli v1.0.6 // indirect
	github.com/armon/go-metrics v0.4.1 // indirect
	github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream v1.5.1 // indirect
	github.com/aws/aws-sdk-go-v2/credentials v1.13.43 // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.13.13 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.1.43 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.4.37 // indirect
	github.com/aws/aws-sdk-go-v2/internal/ini v1.8.0 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.1.3 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.11.1 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/checksum v1.1.35 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/endpoint-discovery v1.7.34 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.9.37 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/s3shared v1.15.3 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.15.2 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.17.3 // indirect
	github.com/aws/aws-sdk-go-v2/service/sts v1.23.2 // indirect
	github.com/aws/smithy-go v1.20.1 // indirect
	github.com/bytedance/sonic v1.10.0 // indirect
	github.com/cenkalti/backoff/v3 v3.2.2 // indirect
	github.com/cespare/xxhash/v2 v2.2.0 // indirect
	github.com/chenzhuoyu/base64x v0.0.0-20230717121745-296ad89f973d // indirect
	github.com/chenzhuoyu/iasm v0.9.0 // indirect
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/eapache/go-resiliency v1.4.0 // indirect
	github.com/eapache/go-xerial-snappy v0.0.0-20230731223053-c322873962e3 // indirect
	github.com/eapache/queue v1.1.0 // indirect
	github.com/ebitengine/purego v0.6.0-alpha.5 // indirect
	github.com/elastic/elastic-transport-go/v8 v8.3.0 // indirect
	github.com/fatih/color v1.15.0 // indirect
	github.com/felixge/httpsnoop v1.0.3 // indirect
	github.com/gabriel-vasile/mimetype v1.4.2 // indirect
	github.com/gin-contrib/sse v0.1.0 // indirect
	github.com/go-jose/go-jose/v3 v3.0.1 // indirect
	github.com/go-logr/logr v1.3.0 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/go-pg/zerochecker v0.2.0 // indirect
	github.com/go-playground/locales v0.14.1 // indirect
	github.com/go-playground/universal-translator v0.18.1 // indirect
	github.com/go-playground/validator/v10 v10.15.1 // indirect
	github.com/goccy/go-json v0.10.2 // indirect
	github.com/gofrs/uuid v4.4.0+incompatible // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/golang-sql/civil v0.0.0-20220223132316-b832511892a9 // indirect
	github.com/golang-sql/sqlexp v0.1.0 // indirect
	github.com/golang/groupcache v0.0.0-20210331224755-41bb18bfe9da // indirect
	github.com/golang/snappy v0.0.4 // indirect
	github.com/google/go-cmp v0.6.0 // indirect
	github.com/google/gofuzz v1.2.0 // indirect
	github.com/google/s2a-go v0.1.7 // indirect
	github.com/googleapis/enterprise-certificate-proxy v0.3.2 // indirect
	github.com/googleapis/gax-go/v2 v2.12.0 // indirect
	github.com/googleapis/gnostic v0.5.5 // indirect
	github.com/gorilla/websocket v1.5.0 // indirect
	github.com/hailocab/go-hostpool v0.0.0-20160125115350-e80d13ce29ed // indirect
	github.com/hashicorp/errwrap v1.1.0 // indirect
	github.com/hashicorp/go-cleanhttp v0.5.2 // indirect
	github.com/hashicorp/go-hclog v1.5.0 // indirect
	github.com/hashicorp/go-immutable-radix v1.3.1 // indirect
	github.com/hashicorp/go-multierror v1.1.1 // indirect
	github.com/hashicorp/go-retryablehttp v0.7.4 // indirect
	github.com/hashicorp/go-rootcerts v1.0.2 // indirect
	github.com/hashicorp/go-secure-stdlib/strutil v0.1.2 // indirect
	github.com/hashicorp/go-sockaddr v1.0.2 // indirect
	github.com/hashicorp/go-uuid v1.0.3 // indirect
	github.com/hashicorp/golang-lru v1.0.2 // indirect
	github.com/hashicorp/golang-lru/v2 v2.0.3 // indirect
	github.com/hashicorp/hcl v1.0.1-vault-5 // indirect
	github.com/hashicorp/serf v0.10.1 // indirect
	github.com/hashicorp/vault/sdk v0.9.2 // indirect
	github.com/imdario/mergo v0.3.12 // indirect
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
	github.com/klauspost/compress v1.17.1 // indirect
	github.com/klauspost/cpuid/v2 v2.2.5 // indirect
	github.com/labstack/gommon v0.4.0 // indirect
	github.com/leodido/go-urn v1.2.4 // indirect
	github.com/mailru/easyjson v0.7.7 // indirect
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/mattn/go-runewidth v0.0.15 // indirect
	github.com/mitchellh/go-homedir v1.1.0 // indirect
	github.com/mitchellh/mapstructure v1.5.0 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/montanaflynn/stats v0.6.6 // indirect
	github.com/outcaste-io/ristretto v0.2.3 // indirect
	github.com/pelletier/go-toml/v2 v2.0.9 // indirect
	github.com/philhofer/fwd v1.1.2 // indirect
	github.com/pierrec/lz4/v4 v4.1.18 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/rcrowley/go-metrics v0.0.0-20201227073835-cf1acfcdf475 // indirect
	github.com/richardartoul/molecule v1.0.1-0.20221107223329-32cfee06a052 // indirect
	github.com/rivo/uniseg v0.4.4 // indirect
	github.com/ryanuber/go-glob v1.0.0 // indirect
	github.com/secure-systems-lab/go-securesystemslib v0.8.0 // indirect
	github.com/spaolacci/murmur3 v1.1.0 // indirect
	github.com/spf13/pflag v1.0.5 // indirect
	github.com/tidwall/btree v1.6.0 // indirect
	github.com/tidwall/gjson v1.16.0 // indirect
	github.com/tidwall/grect v0.1.4 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/tidwall/rtred v0.1.2 // indirect
	github.com/tidwall/tinyqueue v0.1.1 // indirect
	github.com/tmthrgd/go-hex v0.0.0-20190904060850-447a3041c3bc // indirect
	github.com/twitchyliquid64/golang-asm v0.15.1 // indirect
	github.com/ugorji/go/codec v1.2.11 // indirect
	github.com/valyala/bytebufferpool v1.0.0 // indirect
	github.com/valyala/fasttemplate v1.2.2 // indirect
	github.com/valyala/tcplisten v1.0.0 // indirect
	github.com/vektah/gqlparser/v2 v2.5.8 // indirect
	github.com/vmihailenco/bufpool v0.1.11 // indirect
	github.com/vmihailenco/msgpack/v5 v5.3.5 // indirect
	github.com/vmihailenco/tagparser v0.1.2 // indirect
	github.com/vmihailenco/tagparser/v2 v2.0.0 // indirect
	github.com/xdg-go/pbkdf2 v1.0.0 // indirect
	github.com/xdg-go/scram v1.1.2 // indirect
	github.com/xdg-go/stringprep v1.0.4 // indirect
	github.com/youmark/pkcs8 v0.0.0-20181117223130-1be2e3e5546d // indirect
	go.opencensus.io v0.24.0 // indirect
	go.opentelemetry.io/otel/metric v1.20.0 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	golang.org/x/arch v0.4.0 // indirect
	golang.org/x/crypto v0.21.0 // indirect
	golang.org/x/exp v0.0.0-20230321023759-10a507213a29 // indirect
	golang.org/x/mod v0.14.0 // indirect
	golang.org/x/oauth2 v0.13.0 // indirect
	golang.org/x/sync v0.5.0 // indirect
	golang.org/x/sys v0.18.0 // indirect
	golang.org/x/term v0.18.0 // indirect
	golang.org/x/text v0.14.0 // indirect
	golang.org/x/tools v0.16.1 // indirect
	golang.org/x/xerrors v0.0.0-20231012003039-104605ab7028 // indirect
	google.golang.org/appengine v1.6.8 // indirect
	google.golang.org/genproto v0.0.0-20231211222908-989df2bf70f3 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20231120223509-83a465c0220f // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20231212172506-995d672761c0 // indirect
	gopkg.in/inf.v0 v0.9.1 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	k8s.io/api v0.23.17 // indirect
	k8s.io/klog/v2 v2.30.0 // indirect
	k8s.io/kube-openapi v0.0.0-20211115234752-e816edb12b65 // indirect
	k8s.io/utils v0.0.0-20211116205334-6203023598ed // indirect
	mellium.im/sasl v0.3.1 // indirect
	sigs.k8s.io/json v0.0.0-20211020170558-c049b76a60c6 // indirect
	sigs.k8s.io/structured-merge-diff/v4 v4.2.3 // indirect
	sigs.k8s.io/yaml v1.2.0 // indirect
)
