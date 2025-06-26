module github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration

go 1.23.0

require (
	cloud.google.com/go/pubsub v1.37.0
	github.com/99designs/gqlgen v0.17.62
	github.com/DataDog/datadog-agent/pkg/proto v0.66.1
	github.com/DataDog/dd-trace-go/orchestrion/all/v2 v2.2.0-dev
	github.com/DataDog/dd-trace-go/v2 v2.2.0-dev
	github.com/DataDog/go-libddwaf/v4 v4.2.1-0.20250613111738-824b8f59fcd9
	github.com/DataDog/orchestrion v1.4.0
	github.com/IBM/sarama v1.44.0
	github.com/Shopify/sarama v1.38.1
	github.com/aws/aws-sdk-go v1.55.5
	github.com/aws/aws-sdk-go-v2 v1.26.1
	github.com/aws/aws-sdk-go-v2/config v1.27.11
	github.com/aws/aws-sdk-go-v2/credentials v1.17.11
	github.com/aws/aws-sdk-go-v2/service/dynamodb v1.31.1
	github.com/cenkalti/backoff/v4 v4.3.0
	github.com/confluentinc/confluent-kafka-go v1.9.2
	github.com/confluentinc/confluent-kafka-go/v2 v2.4.0
	github.com/docker/go-connections v0.5.0
	github.com/elastic/go-elasticsearch/v6 v6.8.5
	github.com/elastic/go-elasticsearch/v7 v7.17.1
	github.com/elastic/go-elasticsearch/v8 v8.12.1
	github.com/gin-gonic/gin v1.10.0
	github.com/go-chi/chi/v5 v5.2.0
	github.com/go-redis/redis v6.15.9+incompatible
	github.com/go-redis/redis/v7 v7.4.1
	github.com/go-redis/redis/v8 v8.11.5
	github.com/gocql/gocql v1.7.0
	github.com/gofiber/fiber/v2 v2.52.7
	github.com/gomodule/redigo v1.9.2
	github.com/google/uuid v1.6.0
	github.com/gorilla/mux v1.8.1
	github.com/graph-gophers/graphql-go v1.5.0
	github.com/graphql-go/graphql v0.8.1
	github.com/graphql-go/handler v0.2.3
	github.com/hashicorp/vault/api v1.15.0
	github.com/jackc/pgx/v5 v5.7.2
	github.com/julienschmidt/httprouter v1.3.0
	github.com/labstack/echo/v4 v4.13.3
	github.com/mattn/go-sqlite3 v1.14.22
	github.com/redis/go-redis/v9 v9.7.3
	github.com/redis/rueidis v1.0.56
	github.com/segmentio/kafka-go v0.4.42
	github.com/sirupsen/logrus v1.9.3
	github.com/stretchr/testify v1.10.0
	github.com/testcontainers/testcontainers-go v0.37.0
	github.com/testcontainers/testcontainers-go/modules/cassandra v0.37.0
	github.com/testcontainers/testcontainers-go/modules/elasticsearch v0.37.0
	github.com/testcontainers/testcontainers-go/modules/gcloud v0.37.0
	github.com/testcontainers/testcontainers-go/modules/kafka v0.37.0
	github.com/testcontainers/testcontainers-go/modules/mongodb v0.37.0
	github.com/testcontainers/testcontainers-go/modules/postgres v0.37.0
	github.com/testcontainers/testcontainers-go/modules/redis v0.37.0
	github.com/testcontainers/testcontainers-go/modules/valkey v0.35.0
	github.com/testcontainers/testcontainers-go/modules/vault v0.37.0
	github.com/tinylib/msgp v1.2.5
	github.com/twitchtv/twirp v8.1.3+incompatible
	github.com/valkey-io/valkey-go v1.0.56
	github.com/vektah/gqlparser/v2 v2.5.21
	github.com/xlab/treeprint v1.2.0
	go.mongodb.org/mongo-driver v1.17.1
	go.mongodb.org/mongo-driver/v2 v2.2.2
	google.golang.org/api v0.176.1
	google.golang.org/grpc v1.71.1
	google.golang.org/grpc/examples v0.0.0-20240521165117-aea78bdf9d13
	gorm.io/driver/sqlite v1.5.7
	gorm.io/gorm v1.25.12
	gotest.tools/v3 v3.5.2
	k8s.io/apimachinery v0.32.3
	k8s.io/client-go v0.32.2
)

require (
	cloud.google.com/go v0.112.1 // indirect
	cloud.google.com/go/auth v0.7.2 // indirect
	cloud.google.com/go/auth/oauth2adapt v0.2.4 // indirect
	cloud.google.com/go/compute/metadata v0.6.0 // indirect
	cloud.google.com/go/iam v1.1.7 // indirect
	dario.cat/mergo v1.0.1 // indirect
	github.com/Azure/go-ansiterm v0.0.0-20230124172434-306776ec8161 // indirect
	github.com/DataDog/appsec-internal-go v1.12.0 // indirect
	github.com/DataDog/datadog-agent/comp/core/tagger/origindetection v0.66.1 // indirect
	github.com/DataDog/datadog-agent/pkg/obfuscate v0.66.1 // indirect
	github.com/DataDog/datadog-agent/pkg/remoteconfig/state v0.66.1 // indirect
	github.com/DataDog/datadog-agent/pkg/trace v0.66.1 // indirect
	github.com/DataDog/datadog-agent/pkg/util/log v0.66.1 // indirect
	github.com/DataDog/datadog-agent/pkg/util/scrubber v0.66.1 // indirect
	github.com/DataDog/datadog-agent/pkg/version v0.66.1 // indirect
	github.com/DataDog/datadog-go/v5 v5.6.0 // indirect
	github.com/DataDog/dd-trace-go/contrib/99designs/gqlgen/v2 v2.2.0-dev // indirect
	github.com/DataDog/dd-trace-go/contrib/IBM/sarama/v2 v2.2.0-dev // indirect
	github.com/DataDog/dd-trace-go/contrib/Shopify/sarama/v2 v2.2.0-dev // indirect
	github.com/DataDog/dd-trace-go/contrib/aws/aws-sdk-go-v2/v2 v2.2.0-dev // indirect
	github.com/DataDog/dd-trace-go/contrib/aws/aws-sdk-go/v2 v2.2.0-dev // indirect
	github.com/DataDog/dd-trace-go/contrib/cloud.google.com/go/pubsub.v1/v2 v2.2.0-dev // indirect
	github.com/DataDog/dd-trace-go/contrib/confluentinc/confluent-kafka-go/kafka.v2/v2 v2.2.0-dev // indirect
	github.com/DataDog/dd-trace-go/contrib/confluentinc/confluent-kafka-go/kafka/v2 v2.2.0-dev // indirect
	github.com/DataDog/dd-trace-go/contrib/database/sql/v2 v2.2.0-dev // indirect
	github.com/DataDog/dd-trace-go/contrib/elastic/go-elasticsearch.v6/v2 v2.2.0-dev // indirect
	github.com/DataDog/dd-trace-go/contrib/gin-gonic/gin/v2 v2.2.0-dev // indirect
	github.com/DataDog/dd-trace-go/contrib/go-chi/chi.v5/v2 v2.2.0-dev // indirect
	github.com/DataDog/dd-trace-go/contrib/go-chi/chi/v2 v2.2.0-dev // indirect
	github.com/DataDog/dd-trace-go/contrib/go-redis/redis.v7/v2 v2.2.0-dev // indirect
	github.com/DataDog/dd-trace-go/contrib/go-redis/redis.v8/v2 v2.2.0-dev // indirect
	github.com/DataDog/dd-trace-go/contrib/go-redis/redis/v2 v2.2.0-dev // indirect
	github.com/DataDog/dd-trace-go/contrib/go.mongodb.org/mongo-driver.v2/v2 v2.2.0-dev // indirect
	github.com/DataDog/dd-trace-go/contrib/go.mongodb.org/mongo-driver/v2 v2.2.0-dev // indirect
	github.com/DataDog/dd-trace-go/contrib/gocql/gocql/v2 v2.2.0-dev // indirect
	github.com/DataDog/dd-trace-go/contrib/gofiber/fiber.v2/v2 v2.2.0-dev // indirect
	github.com/DataDog/dd-trace-go/contrib/gomodule/redigo/v2 v2.2.0-dev // indirect
	github.com/DataDog/dd-trace-go/contrib/google.golang.org/grpc/v2 v2.2.0-dev // indirect
	github.com/DataDog/dd-trace-go/contrib/gorilla/mux/v2 v2.2.0-dev // indirect
	github.com/DataDog/dd-trace-go/contrib/gorm.io/gorm.v1/v2 v2.2.0-dev // indirect
	github.com/DataDog/dd-trace-go/contrib/graph-gophers/graphql-go/v2 v2.2.0-dev // indirect
	github.com/DataDog/dd-trace-go/contrib/graphql-go/graphql/v2 v2.2.0-dev // indirect
	github.com/DataDog/dd-trace-go/contrib/hashicorp/vault/v2 v2.2.0-dev // indirect
	github.com/DataDog/dd-trace-go/contrib/jackc/pgx.v5/v2 v2.2.0-dev // indirect
	github.com/DataDog/dd-trace-go/contrib/julienschmidt/httprouter/v2 v2.2.0-dev // indirect
	github.com/DataDog/dd-trace-go/contrib/k8s.io/client-go/v2 v2.2.0-dev // indirect
	github.com/DataDog/dd-trace-go/contrib/labstack/echo.v4/v2 v2.2.0-dev // indirect
	github.com/DataDog/dd-trace-go/contrib/log/slog/v2 v2.2.0-dev // indirect
	github.com/DataDog/dd-trace-go/contrib/net/http/v2 v2.2.0-dev // indirect
	github.com/DataDog/dd-trace-go/contrib/redis/go-redis.v9/v2 v2.2.0-dev // indirect
	github.com/DataDog/dd-trace-go/contrib/redis/rueidis/v2 v2.2.0-dev // indirect
	github.com/DataDog/dd-trace-go/contrib/segmentio/kafka-go/v2 v2.2.0-dev // indirect
	github.com/DataDog/dd-trace-go/contrib/sirupsen/logrus/v2 v2.2.0-dev // indirect
	github.com/DataDog/dd-trace-go/contrib/twitchtv/twirp/v2 v2.2.0-dev // indirect
	github.com/DataDog/dd-trace-go/contrib/valkey-io/valkey-go/v2 v2.2.0-dev // indirect
	github.com/DataDog/go-runtime-metrics-internal v0.0.4-0.20250603194815-7edb7c2ad56a // indirect
	github.com/DataDog/go-sqllexer v0.1.6 // indirect
	github.com/DataDog/go-tuf v1.1.0-0.5.2 // indirect
	github.com/DataDog/gostackparse v0.7.0 // indirect
	github.com/DataDog/opentelemetry-mapping-go/pkg/otlp/attributes v0.26.0 // indirect
	github.com/DataDog/sketches-go v1.4.7 // indirect
	github.com/Masterminds/semver/v3 v3.3.1 // indirect
	github.com/Microsoft/go-winio v0.6.2 // indirect
	github.com/Microsoft/hcsshim v0.11.7 // indirect
	github.com/agnivade/levenshtein v1.2.0 // indirect
	github.com/andybalholm/brotli v1.1.1 // indirect
	github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream v1.6.2 // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.16.1 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.3.5 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.6.5 // indirect
	github.com/aws/aws-sdk-go-v2/internal/ini v1.8.3 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.3.5 // indirect
	github.com/aws/aws-sdk-go-v2/service/eventbridge v1.30.4 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.11.2 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/checksum v1.3.7 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/endpoint-discovery v1.9.6 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.11.7 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/s3shared v1.17.5 // indirect
	github.com/aws/aws-sdk-go-v2/service/kinesis v1.27.4 // indirect
	github.com/aws/aws-sdk-go-v2/service/s3 v1.53.1 // indirect
	github.com/aws/aws-sdk-go-v2/service/sfn v1.26.4 // indirect
	github.com/aws/aws-sdk-go-v2/service/sns v1.29.4 // indirect
	github.com/aws/aws-sdk-go-v2/service/sqs v1.31.4 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.20.5 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.23.4 // indirect
	github.com/aws/aws-sdk-go-v2/service/sts v1.28.6 // indirect
	github.com/aws/smithy-go v1.20.2 // indirect
	github.com/aymanbagabas/go-osc52/v2 v2.0.1 // indirect
	github.com/blakesmith/ar v0.0.0-20190502131153-809d4375e1fb // indirect
	github.com/bytedance/sonic v1.12.6 // indirect
	github.com/bytedance/sonic/loader v0.2.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/charmbracelet/colorprofile v0.3.0 // indirect
	github.com/charmbracelet/lipgloss v1.1.0 // indirect
	github.com/charmbracelet/x/ansi v0.8.0 // indirect
	github.com/charmbracelet/x/cellbuf v0.0.13 // indirect
	github.com/charmbracelet/x/term v0.2.1 // indirect
	github.com/cihub/seelog v0.0.0-20170130134532-f561c5e57575 // indirect
	github.com/cloudwego/base64x v0.1.4 // indirect
	github.com/cloudwego/iasm v0.2.0 // indirect
	github.com/containerd/continuity v0.4.4 // indirect
	github.com/containerd/log v0.1.0 // indirect
	github.com/containerd/platforms v0.2.1 // indirect
	github.com/containerd/ttrpc v1.2.7 // indirect
	github.com/cpuguy83/dockercfg v0.3.2 // indirect
	github.com/cpuguy83/go-md2man/v2 v2.0.6 // indirect
	github.com/dave/dst v0.27.3 // indirect
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/distribution/reference v0.6.0 // indirect
	github.com/docker/docker v28.0.1+incompatible // indirect
	github.com/docker/go-units v0.5.0 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/eapache/go-resiliency v1.7.0 // indirect
	github.com/eapache/go-xerial-snappy v0.0.0-20230731223053-c322873962e3 // indirect
	github.com/eapache/queue v1.1.0 // indirect
	github.com/eapache/queue/v2 v2.0.0-20230407133247-75960ed334e4 // indirect
	github.com/ebitengine/purego v0.8.3 // indirect
	github.com/elastic/elastic-transport-go/v8 v8.4.0 // indirect
	github.com/emicklei/go-restful/v3 v3.11.0 // indirect
	github.com/felixge/httpsnoop v1.0.4 // indirect
	github.com/fsnotify/fsnotify v1.9.0 // indirect
	github.com/fxamacker/cbor/v2 v2.7.0 // indirect
	github.com/gabriel-vasile/mimetype v1.4.8 // indirect
	github.com/gin-contrib/sse v1.0.0 // indirect
	github.com/go-chi/chi v1.5.4 // indirect
	github.com/go-jose/go-jose/v4 v4.0.5 // indirect
	github.com/go-logr/logr v1.4.2 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/go-ole/go-ole v1.3.0 // indirect
	github.com/go-openapi/jsonpointer v0.21.0 // indirect
	github.com/go-openapi/jsonreference v0.20.2 // indirect
	github.com/go-openapi/swag v0.23.0 // indirect
	github.com/go-playground/locales v0.14.1 // indirect
	github.com/go-playground/universal-translator v0.18.1 // indirect
	github.com/go-playground/validator/v10 v10.23.0 // indirect
	github.com/go-viper/mapstructure/v2 v2.2.1 // indirect
	github.com/goccy/go-json v0.10.4 // indirect
	github.com/goccy/go-yaml v1.17.1 // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/golang/groupcache v0.0.0-20210331224755-41bb18bfe9da // indirect
	github.com/golang/protobuf v1.5.4 // indirect
	github.com/golang/snappy v1.0.0 // indirect
	github.com/google/gnostic-models v0.6.8 // indirect
	github.com/google/go-cmp v0.7.0 // indirect
	github.com/google/go-tpm v0.9.3 // indirect
	github.com/google/gofuzz v1.2.0 // indirect
	github.com/google/pprof v0.0.0-20250403155104-27863c87afa6 // indirect
	github.com/google/s2a-go v0.1.7 // indirect
	github.com/googleapis/enterprise-certificate-proxy v0.3.6 // indirect
	github.com/googleapis/gax-go/v2 v2.12.4 // indirect
	github.com/gorilla/websocket v1.5.3 // indirect
	github.com/hailocab/go-hostpool v0.0.0-20160125115350-e80d13ce29ed // indirect
	github.com/hashicorp/errwrap v1.1.0 // indirect
	github.com/hashicorp/go-cleanhttp v0.5.2 // indirect
	github.com/hashicorp/go-multierror v1.1.1 // indirect
	github.com/hashicorp/go-retryablehttp v0.7.7 // indirect
	github.com/hashicorp/go-rootcerts v1.0.2 // indirect
	github.com/hashicorp/go-secure-stdlib/parseutil v0.2.0 // indirect
	github.com/hashicorp/go-secure-stdlib/strutil v0.1.2 // indirect
	github.com/hashicorp/go-sockaddr v1.0.7 // indirect
	github.com/hashicorp/go-uuid v1.0.3 // indirect
	github.com/hashicorp/golang-lru/v2 v2.0.7 // indirect
	github.com/hashicorp/hcl v1.0.1-vault-7 // indirect
	github.com/hashicorp/vault/sdk v0.14.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
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
	github.com/klauspost/compress v1.18.0 // indirect
	github.com/klauspost/cpuid/v2 v2.2.9 // indirect
	github.com/labstack/gommon v0.4.2 // indirect
	github.com/leodido/go-urn v1.4.0 // indirect
	github.com/lucasb-eyer/go-colorful v1.2.0 // indirect
	github.com/lufia/plan9stats v0.0.0-20250317134145-8bc96cf8fc35 // indirect
	github.com/magiconair/properties v1.8.10 // indirect
	github.com/mailru/easyjson v0.7.7 // indirect
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/mattn/go-runewidth v0.0.16 // indirect
	github.com/mdelapenya/tlscert v0.2.0 // indirect
	github.com/minio/highwayhash v1.0.3 // indirect
	github.com/mitchellh/go-homedir v1.1.0 // indirect
	github.com/mitchellh/mapstructure v1.5.1-0.20231216201459-8508981c8b6c // indirect
	github.com/moby/docker-image-spec v1.3.1 // indirect
	github.com/moby/patternmatcher v0.6.0 // indirect
	github.com/moby/sys/sequential v0.6.0 // indirect
	github.com/moby/sys/user v0.4.0 // indirect
	github.com/moby/sys/userns v0.1.0 // indirect
	github.com/moby/term v0.5.0 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/montanaflynn/stats v0.7.1 // indirect
	github.com/morikuni/aec v1.0.0 // indirect
	github.com/muesli/termenv v0.16.0 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/nats-io/jwt/v2 v2.7.3 // indirect
	github.com/nats-io/nats-server/v2 v2.11.1 // indirect
	github.com/nats-io/nats.go v1.41.1 // indirect
	github.com/nats-io/nkeys v0.4.10 // indirect
	github.com/nats-io/nuid v1.0.1 // indirect
	github.com/opencontainers/go-digest v1.0.0 // indirect
	github.com/opencontainers/image-spec v1.1.1 // indirect
	github.com/outcaste-io/ristretto v0.2.3 // indirect
	github.com/pelletier/go-toml/v2 v2.2.3 // indirect
	github.com/philhofer/fwd v1.1.3-0.20240916144458-20a13a1f6b7c // indirect
	github.com/pierrec/lz4/v4 v4.1.22 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/planetscale/vtprotobuf v0.6.1-0.20240319094008-0393e58bdf10 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/power-devops/perfstat v0.0.0-20240221224432-82ca36839d55 // indirect
	github.com/prometheus/client_golang v1.20.2 // indirect
	github.com/prometheus/client_model v0.6.1 // indirect
	github.com/puzpuzpuz/xsync/v3 v3.5.1 // indirect
	github.com/rcrowley/go-metrics v0.0.0-20201227073835-cf1acfcdf475 // indirect
	github.com/richardartoul/molecule v1.0.1-0.20240531184615-7ca0df43c0b3 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/rs/zerolog v1.34.0 // indirect
	github.com/russross/blackfriday/v2 v2.1.0 // indirect
	github.com/ryanuber/go-glob v1.0.0 // indirect
	github.com/secure-systems-lab/go-securesystemslib v0.9.0 // indirect
	github.com/shirou/gopsutil/v4 v4.25.3 // indirect
	github.com/sosodev/duration v1.3.1 // indirect
	github.com/spaolacci/murmur3 v1.1.0 // indirect
	github.com/spf13/pflag v1.0.5 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/tklauser/go-sysconf v0.3.15 // indirect
	github.com/tklauser/numcpus v0.10.0 // indirect
	github.com/twitchyliquid64/golang-asm v0.15.1 // indirect
	github.com/ugorji/go/codec v1.2.12 // indirect
	github.com/urfave/cli/v2 v2.27.6 // indirect
	github.com/valyala/bytebufferpool v1.0.0 // indirect
	github.com/valyala/fasthttp v1.58.0 // indirect
	github.com/valyala/fasttemplate v1.2.2 // indirect
	github.com/valyala/tcplisten v1.0.0 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	github.com/xdg-go/pbkdf2 v1.0.0 // indirect
	github.com/xdg-go/scram v1.1.2 // indirect
	github.com/xdg-go/stringprep v1.0.4 // indirect
	github.com/xeipuuv/gojsonpointer v0.0.0-20190905194746-02993c407bfb // indirect
	github.com/xeipuuv/gojsonreference v0.0.0-20180127040603-bd5ef7bd5415 // indirect
	github.com/xeipuuv/gojsonschema v1.2.0 // indirect
	github.com/xo/terminfo v0.0.0-20220910002029-abceb7e1c41e // indirect
	github.com/xrash/smetrics v0.0.0-20240521201337-686a1a2994c1 // indirect
	github.com/youmark/pkcs8 v0.0.0-20240726163527-a2c0da244d78 // indirect
	github.com/yusufpapurcu/wmi v1.2.4 // indirect
	go.opencensus.io v0.24.0 // indirect
	go.opentelemetry.io/auto/sdk v1.1.0 // indirect
	go.opentelemetry.io/collector/component v1.28.1 // indirect
	go.opentelemetry.io/collector/pdata v1.29.0 // indirect
	go.opentelemetry.io/collector/pdata/pprofile v0.123.0 // indirect
	go.opentelemetry.io/collector/semconv v0.123.0 // indirect
	go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc v0.59.0 // indirect
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.60.0 // indirect
	go.opentelemetry.io/otel v1.35.0 // indirect
	go.opentelemetry.io/otel/metric v1.35.0 // indirect
	go.opentelemetry.io/otel/trace v1.35.0 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.uber.org/zap v1.27.0 // indirect
	golang.org/x/arch v0.13.0 // indirect
	golang.org/x/crypto v0.37.0 // indirect
	golang.org/x/exp v0.0.0-20250210185358-939b2ce775ac // indirect
	golang.org/x/mod v0.24.0 // indirect
	golang.org/x/net v0.39.0 // indirect
	golang.org/x/oauth2 v0.29.0 // indirect
	golang.org/x/sync v0.13.0 // indirect
	golang.org/x/sys v0.33.0 // indirect
	golang.org/x/term v0.31.0 // indirect
	golang.org/x/text v0.24.0 // indirect
	golang.org/x/time v0.11.0 // indirect
	golang.org/x/tools v0.32.0 // indirect
	golang.org/x/xerrors v0.0.0-20240903120638-7835f813f4da // indirect
	google.golang.org/genproto v0.0.0-20240325203815-454cdb8f5daa // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20250219182151-9fdb1cabc7b2 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20250428153025-10db94c68c34 // indirect
	google.golang.org/protobuf v1.36.6 // indirect
	gopkg.in/evanphx/json-patch.v4 v4.12.0 // indirect
	gopkg.in/inf.v0 v0.9.1 // indirect
	gopkg.in/ini.v1 v1.67.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	k8s.io/api v0.32.2 // indirect
	k8s.io/klog/v2 v2.130.1 // indirect
	k8s.io/kube-openapi v0.0.0-20241105132330-32ad38e42d3f // indirect
	k8s.io/utils v0.0.0-20241210054802-24370beab758 // indirect
	sigs.k8s.io/json v0.0.0-20241010143419-9aa6b5e7a4b3 // indirect
	sigs.k8s.io/structured-merge-diff/v4 v4.4.2 // indirect
	sigs.k8s.io/yaml v1.4.0 // indirect
	tags.cncf.io/container-device-interface v0.8.1 // indirect
)

replace github.com/DataDog/dd-trace-go/contrib/99designs/gqlgen/v2 => ../../../contrib/99designs/gqlgen

replace github.com/DataDog/dd-trace-go/contrib/IBM/sarama/v2 => ../../../contrib/IBM/sarama

replace github.com/DataDog/dd-trace-go/contrib/Shopify/sarama/v2 => ../../../contrib/Shopify/sarama

replace github.com/DataDog/dd-trace-go/contrib/aws/aws-sdk-go-v2/v2 => ../../../contrib/aws/aws-sdk-go-v2

replace github.com/DataDog/dd-trace-go/contrib/aws/aws-sdk-go/v2 => ../../../contrib/aws/aws-sdk-go

replace github.com/DataDog/dd-trace-go/contrib/cloud.google.com/go/pubsub.v1/v2 => ../../../contrib/cloud.google.com/go/pubsub.v1

replace github.com/DataDog/dd-trace-go/contrib/confluentinc/confluent-kafka-go/kafka.v2/v2 => ../../../contrib/confluentinc/confluent-kafka-go/kafka.v2

replace github.com/DataDog/dd-trace-go/contrib/confluentinc/confluent-kafka-go/kafka/v2 => ../../../contrib/confluentinc/confluent-kafka-go/kafka

replace github.com/DataDog/dd-trace-go/contrib/database/sql/v2 => ../../../contrib/database/sql

replace github.com/DataDog/dd-trace-go/contrib/elastic/go-elasticsearch.v6/v2 => ../../../contrib/elastic/go-elasticsearch.v6

replace github.com/DataDog/dd-trace-go/contrib/gin-gonic/gin/v2 => ../../../contrib/gin-gonic/gin

replace github.com/DataDog/dd-trace-go/contrib/go-chi/chi.v5/v2 => ../../../contrib/go-chi/chi.v5

replace github.com/DataDog/dd-trace-go/contrib/go-chi/chi/v2 => ../../../contrib/go-chi/chi

replace github.com/DataDog/dd-trace-go/contrib/go-redis/redis.v7/v2 => ../../../contrib/go-redis/redis.v7

replace github.com/DataDog/dd-trace-go/contrib/go-redis/redis.v8/v2 => ../../../contrib/go-redis/redis.v8

replace github.com/DataDog/dd-trace-go/contrib/go-redis/redis/v2 => ../../../contrib/go-redis/redis

replace github.com/DataDog/dd-trace-go/contrib/go.mongodb.org/mongo-driver/v2 => ../../../contrib/go.mongodb.org/mongo-driver

replace github.com/DataDog/dd-trace-go/contrib/gocql/gocql/v2 => ../../../contrib/gocql/gocql

replace github.com/DataDog/dd-trace-go/contrib/gofiber/fiber.v2/v2 => ../../../contrib/gofiber/fiber.v2

replace github.com/DataDog/dd-trace-go/contrib/gomodule/redigo/v2 => ../../../contrib/gomodule/redigo

replace github.com/DataDog/dd-trace-go/contrib/google.golang.org/grpc/v2 => ../../../contrib/google.golang.org/grpc

replace github.com/DataDog/dd-trace-go/contrib/gorilla/mux/v2 => ../../../contrib/gorilla/mux

replace github.com/DataDog/dd-trace-go/contrib/gorm.io/gorm.v1/v2 => ../../../contrib/gorm.io/gorm.v1

replace github.com/DataDog/dd-trace-go/contrib/graph-gophers/graphql-go/v2 => ../../../contrib/graph-gophers/graphql-go

replace github.com/DataDog/dd-trace-go/contrib/graphql-go/graphql/v2 => ../../../contrib/graphql-go/graphql

replace github.com/DataDog/dd-trace-go/contrib/hashicorp/vault/v2 => ../../../contrib/hashicorp/vault

replace github.com/DataDog/dd-trace-go/contrib/jackc/pgx.v5/v2 => ../../../contrib/jackc/pgx.v5

replace github.com/DataDog/dd-trace-go/contrib/julienschmidt/httprouter/v2 => ../../../contrib/julienschmidt/httprouter

replace github.com/DataDog/dd-trace-go/contrib/k8s.io/client-go/v2 => ../../../contrib/k8s.io/client-go

replace github.com/DataDog/dd-trace-go/contrib/labstack/echo.v4/v2 => ../../../contrib/labstack/echo.v4

replace github.com/DataDog/dd-trace-go/contrib/log/slog/v2 => ../../../contrib/log/slog

replace github.com/DataDog/dd-trace-go/contrib/net/http/v2 => ../../../contrib/net/http

replace github.com/DataDog/dd-trace-go/contrib/redis/go-redis.v9/v2 => ../../../contrib/redis/go-redis.v9

replace github.com/DataDog/dd-trace-go/contrib/redis/rueidis/v2 => ../../../contrib/redis/rueidis

replace github.com/DataDog/dd-trace-go/contrib/segmentio/kafka-go/v2 => ../../../contrib/segmentio/kafka-go

replace github.com/DataDog/dd-trace-go/contrib/sirupsen/logrus/v2 => ../../../contrib/sirupsen/logrus

replace github.com/DataDog/dd-trace-go/contrib/twitchtv/twirp/v2 => ../../../contrib/twitchtv/twirp

replace github.com/DataDog/dd-trace-go/contrib/valkey-io/valkey-go/v2 => ../../../contrib/valkey-io/valkey-go

replace github.com/DataDog/dd-trace-go/orchestrion/all/v2 => ../../../orchestrion/all

replace github.com/DataDog/dd-trace-go/v2 => ../../..

replace google.golang.org/grpc => google.golang.org/grpc v1.70.0

replace github.com/DataDog/dd-trace-go/contrib/go.mongodb.org/mongo-driver.v2/v2 => ../../../contrib/go.mongodb.org/mongo-driver.v2
