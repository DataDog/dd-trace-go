module gopkg.in/DataDog/dd-trace-go.v1

go 1.16

replace (
    github.com/apache/thrift => github.com/apache/thrift v0.13.0
)

require (
	cloud.google.com/go v0.103.0 // indirect
	cloud.google.com/go/pubsub v1.24.0
	github.com/BurntSushi/toml v1.1.0 // indirect
	github.com/DataDog/datadog-agent/pkg/obfuscate v0.37.1
	github.com/DataDog/datadog-go v4.8.3+incompatible // indirect
	github.com/DataDog/datadog-go/v5 v5.1.1
	github.com/DataDog/gostackparse v0.5.0
	github.com/DataDog/sketches-go v1.4.1
	github.com/DataDog/zstd v1.5.2 // indirect
	github.com/Microsoft/go-winio v0.5.2 // indirect
	github.com/Shopify/sarama v1.34.1
	github.com/apache/thrift v0.12.0 // indirect
	github.com/armon/go-metrics v0.4.0 // indirect
	github.com/aws/aws-sdk-go v1.44.57
	github.com/aws/aws-sdk-go-v2 v1.16.7
	github.com/aws/aws-sdk-go-v2/config v1.0.0
	github.com/aws/aws-sdk-go-v2/service/sqs v1.0.0
	github.com/aws/smithy-go v1.12.0
	github.com/bradfitz/gomemcache v0.0.0-20220106215444-fb4bf637b56d
	github.com/cenkalti/backoff/v3 v3.2.2 // indirect
	github.com/cespare/xxhash/v2 v2.1.2 // indirect
	github.com/confluentinc/confluent-kafka-go v1.9.1
	github.com/denisenkom/go-mssqldb v0.11.0
	github.com/eapache/go-resiliency v1.3.0 // indirect
	github.com/elastic/go-elasticsearch/v6 v6.8.5
	github.com/elastic/go-elasticsearch/v7 v7.17.1
	github.com/emicklei/go-restful v2.16.0+incompatible
	github.com/garyburd/redigo v1.6.3
	github.com/gin-gonic/gin v1.8.1
	github.com/globalsign/mgo v0.0.0-20181015135952-eeefdecb41b8
	github.com/go-chi/chi v1.5.4
	github.com/go-chi/chi/v5 v5.0.7
	github.com/go-pg/pg/v10 v10.10.6
	github.com/go-playground/validator/v10 v10.11.0 // indirect
	github.com/go-redis/redis v6.15.9+incompatible
	github.com/go-redis/redis/v7 v7.4.1
	github.com/go-redis/redis/v8 v8.11.5
	github.com/go-sql-driver/mysql v1.6.0
	github.com/go-stack/stack v1.8.1 // indirect
	github.com/goccy/go-json v0.9.10 // indirect
	github.com/gocql/gocql v1.2.0
	github.com/gofiber/fiber/v2 v2.35.0
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/golang/glog v1.0.0 // indirect
	github.com/golang/groupcache v0.0.0-20210331224755-41bb18bfe9da // indirect
	github.com/golang/protobuf v1.5.2
	github.com/golang/snappy v0.0.4 // indirect
	github.com/gomodule/redigo v1.8.9
	github.com/google/pprof v0.0.0-20220608213341-c488b8fa1db3
	github.com/google/uuid v1.3.0
	github.com/gorilla/mux v1.8.0
	github.com/graph-gophers/graphql-go v1.4.0
	github.com/hashicorp/consul/api v1.13.1
	github.com/hashicorp/consul/internal v0.1.0 // indirect
	github.com/hashicorp/errwrap v1.1.0 // indirect
	github.com/hashicorp/go-cleanhttp v0.5.2 // indirect
	github.com/hashicorp/go-hclog v1.2.1 // indirect
	github.com/hashicorp/go-immutable-radix v1.3.1 // indirect
	github.com/hashicorp/go-multierror v1.1.1 // indirect
	github.com/hashicorp/go-plugin v1.4.4 // indirect
	github.com/hashicorp/go-retryablehttp v0.7.1 // indirect
	github.com/hashicorp/go-secure-stdlib/mlock v0.1.2 // indirect
	github.com/hashicorp/go-uuid v1.0.3 // indirect
	github.com/hashicorp/go-version v1.6.0 // indirect
	github.com/hashicorp/go.net v0.0.1 // indirect
	github.com/hashicorp/golang-lru v0.5.4 // indirect
	github.com/hashicorp/serf v0.9.8 // indirect
	github.com/hashicorp/vault/api v1.7.2
	github.com/hashicorp/vault/sdk v0.5.3
	github.com/hashicorp/yamux v0.1.0 // indirect
	github.com/jackc/pgx/v4 v4.14.0
	github.com/jinzhu/gorm v1.9.16
	github.com/jinzhu/now v1.1.5 // indirect
	github.com/jmoiron/sqlx v1.3.5
	github.com/jstemmer/go-junit-report v1.0.0 // indirect
	github.com/julienschmidt/httprouter v1.3.0
	github.com/klauspost/compress v1.15.8 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/labstack/echo v3.3.10+incompatible
	github.com/labstack/echo/v4 v4.7.2
	github.com/labstack/gommon v0.3.1 // indirect
	github.com/lib/pq v1.10.2
	github.com/mailru/easyjson v0.7.7 // indirect
	github.com/mattn/go-colorable v0.1.12 // indirect
	github.com/mattn/go-sqlite3 v1.14.12
	github.com/miekg/dns v1.1.50
	github.com/mitchellh/copystructure v1.2.0 // indirect
	github.com/mitchellh/go-testing-interface v1.14.1 // indirect
	github.com/mitchellh/gox v0.4.0 // indirect
	github.com/mitchellh/iochan v1.0.0 // indirect
	github.com/oklog/run v1.1.0 // indirect
	github.com/opentracing/opentracing-go v1.2.0
	github.com/openzipkin/zipkin-go v0.1.6 // indirect
	github.com/pelletier/go-toml/v2 v2.0.2 // indirect
	github.com/philhofer/fwd v1.1.1 // indirect
	github.com/pierrec/lz4 v2.6.1+incompatible // indirect
	github.com/pierrec/lz4/v4 v4.1.15 // indirect
	github.com/pkg/profile v1.2.1 // indirect
	github.com/segmentio/kafka-go v0.4.32
	github.com/sirupsen/logrus v1.9.0
	github.com/stretchr/testify v1.8.0
	github.com/syndtr/goleveldb v1.0.0
	github.com/tidwall/btree v1.3.1 // indirect
	github.com/tidwall/buntdb v1.2.9
	github.com/tidwall/gjson v1.14.1 // indirect
	github.com/tidwall/grect v0.1.4 // indirect
	github.com/tinylib/msgp v1.1.6
	github.com/twitchtv/twirp v8.1.2+incompatible
	github.com/urfave/negroni v1.0.0
	github.com/vmihailenco/msgpack/v4 v4.3.11 // indirect
	github.com/vmihailenco/msgpack/v5 v5.3.5 // indirect
	github.com/vmihailenco/tagparser v0.1.2 // indirect
	github.com/zenazn/goji v1.0.1
	go.mongodb.org/mongo-driver v1.10.0
	go.opentelemetry.io/otel v1.8.0 // indirect
	go4.org/intern v0.0.0-20220617035311-6925f38cc365 // indirect
	golang.org/x/exp v0.0.0-20220713135740-79cabaa25d75 // indirect
	golang.org/x/net v0.0.0-20220708220712-1185a9018129
	golang.org/x/oauth2 v0.0.0-20220718184931-c8730f7fcb92
	golang.org/x/sys v0.0.0-20220715151400-c0bba94af5f8
	golang.org/x/time v0.0.0-20220609170525-579cf78fd858
	golang.org/x/tools v0.1.11 // indirect
	golang.org/x/xerrors v0.0.0-20220609144429-65e65417b02f
	google.golang.org/api v0.87.0
	google.golang.org/genproto v0.0.0-20220718134204-073382fd740c // indirect
	google.golang.org/grpc v1.48.0
	google.golang.org/protobuf v1.28.0
	gopkg.in/jinzhu/gorm.v1 v1.9.2
	gopkg.in/olivere/elastic.v3 v3.0.75
	gopkg.in/olivere/elastic.v5 v5.0.84
	gopkg.in/square/go-jose.v2 v2.6.0 // indirect
	gorm.io/driver/mysql v1.0.1
	gorm.io/driver/postgres v1.0.0
	gorm.io/driver/sqlserver v1.0.4
	gorm.io/gorm v1.23.8
	honnef.co/go/tools v0.3.2 // indirect
	inet.af/netaddr v0.0.0-20220617031823-097006376321
	k8s.io/apimachinery v0.17.0
	k8s.io/client-go v0.17.0
)
