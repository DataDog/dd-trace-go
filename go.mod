module gopkg.in/DataDog/dd-trace-go.v1

go 1.16

require (
	cloud.google.com/go/pubsub v1.4.0
	github.com/DataDog/datadog-agent/pkg/obfuscate v0.0.0-20211129110424-6491aa3bf583
	github.com/DataDog/datadog-go/v5 v5.0.2
	github.com/DataDog/gostackparse v0.5.0
	github.com/DataDog/sketches-go v1.2.1
	github.com/Shopify/sarama v1.22.0
	github.com/aws/aws-sdk-go v1.34.28
	github.com/aws/aws-sdk-go-v2 v1.0.0
	github.com/aws/aws-sdk-go-v2/config v1.0.0
	github.com/aws/aws-sdk-go-v2/service/sqs v1.0.0
	github.com/aws/smithy-go v1.11.0
	github.com/bradfitz/gomemcache v0.0.0-20220106215444-fb4bf637b56d
	github.com/cespare/xxhash/v2 v2.1.2 // indirect
	github.com/confluentinc/confluent-kafka-go v1.4.0
	github.com/denisenkom/go-mssqldb v0.11.0
	github.com/elastic/go-elasticsearch/v6 v6.8.5
	github.com/elastic/go-elasticsearch/v7 v7.17.1
	github.com/emicklei/go-restful v0.0.0-20170410110728-ff4f55a20633
	github.com/erikstmartin/go-testdb v0.0.0-20160219214506-8d10e4a1bae5 // indirect
	github.com/fatih/color v1.9.0 // indirect
	github.com/frankban/quicktest v1.13.0 // indirect
	github.com/garyburd/redigo v1.6.3
	github.com/gin-gonic/gin v1.7.0
	github.com/globalsign/mgo v0.0.0-20181015135952-eeefdecb41b8
	github.com/go-chi/chi v1.5.0
	github.com/go-chi/chi/v5 v5.0.0
	github.com/go-pg/pg/v10 v10.0.0
	github.com/go-redis/redis v6.15.9+incompatible
	github.com/go-redis/redis/v7 v7.1.0
	github.com/go-redis/redis/v8 v8.0.0
	github.com/go-sql-driver/mysql v1.6.0
	github.com/gocql/gocql v0.0.0-20220224095938-0eacd3183625
	github.com/gofiber/fiber/v2 v2.11.0
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/golang/protobuf v1.5.2
	github.com/golang/snappy v0.0.4 // indirect
	github.com/gomodule/redigo v1.7.0
	github.com/google/pprof v0.0.0-20210423192551-a2663126120b
	github.com/google/uuid v1.3.0
	github.com/gorilla/context v1.1.1 // indirect
	github.com/gorilla/mux v1.5.0
	github.com/graph-gophers/graphql-go v1.3.0
	github.com/hashicorp/consul/api v1.0.0
	github.com/hashicorp/errwrap v1.1.0 // indirect
	github.com/hashicorp/go-cleanhttp v0.5.2 // indirect
	github.com/hashicorp/go-hclog v0.16.2 // indirect
	github.com/hashicorp/go-immutable-radix v1.3.1 // indirect
	github.com/hashicorp/go-multierror v1.1.1 // indirect
	github.com/hashicorp/golang-lru v0.5.4 // indirect
	github.com/hashicorp/memberlist v0.1.6 // indirect
	github.com/hashicorp/serf v0.8.6 // indirect
	github.com/hashicorp/vault/api v1.1.0
	github.com/hashicorp/vault/sdk v0.1.14-0.20200519221838-e0cfd64bc267
	github.com/jackc/pgx/v4 v4.14.0
	github.com/jinzhu/gorm v1.9.1
	github.com/jinzhu/now v1.1.3 // indirect
	github.com/jmoiron/sqlx v1.2.0
	github.com/julienschmidt/httprouter v1.1.0
	github.com/kr/text v0.2.0 // indirect
	github.com/labstack/echo v3.3.10+incompatible
	github.com/labstack/echo/v4 v4.2.0
	github.com/labstack/gommon v0.3.1 // indirect
	github.com/lib/pq v1.10.2
	github.com/mattn/go-sqlite3 v1.14.12
	github.com/miekg/dns v1.1.25
	github.com/mitchellh/mapstructure v1.4.2 // indirect
	github.com/onsi/gomega v1.16.0 // indirect
	github.com/opentracing/opentracing-go v1.2.0
	github.com/philhofer/fwd v1.1.1 // indirect
	github.com/pierrec/lz4 v2.5.2+incompatible // indirect
	github.com/segmentio/kafka-go v0.4.29
	github.com/sirupsen/logrus v1.7.0
	github.com/stretchr/testify v1.7.0
	github.com/syndtr/goleveldb v1.0.0
	github.com/tidwall/btree v1.1.0 // indirect
	github.com/tidwall/buntdb v1.2.0
	github.com/tidwall/grect v0.1.4 // indirect
	github.com/tinylib/msgp v1.1.2
	github.com/twitchtv/twirp v8.1.1+incompatible
	github.com/urfave/negroni v1.0.0
	github.com/valyala/fasthttp v1.34.0 // indirect
	github.com/vmihailenco/msgpack/v5 v5.3.4 // indirect
	github.com/vmihailenco/tagparser v0.1.2 // indirect
	github.com/zenazn/goji v1.0.1
	go.mongodb.org/mongo-driver v1.5.1
	go.opencensus.io v0.22.4 // indirect
	golang.org/x/net v0.0.0-20220225172249-27dd8689420f
	golang.org/x/oauth2 v0.0.0-20200107190931-bf48bf16ab8d
	golang.org/x/sys v0.0.0-20220227234510-4e6760a101f9
	golang.org/x/time v0.0.0-20211116232009-f0f3c7e86c11
	golang.org/x/xerrors v0.0.0-20200804184101-5ec99f83aff1
	google.golang.org/api v0.29.0
	google.golang.org/genproto v0.0.0-20200726014623-da3ae01ef02d // indirect
	google.golang.org/grpc v1.32.0
	google.golang.org/protobuf v1.27.1
	gopkg.in/jinzhu/gorm.v1 v1.9.1
	gopkg.in/olivere/elastic.v3 v3.0.75
	gopkg.in/olivere/elastic.v5 v5.0.84
	gorm.io/driver/mysql v1.0.1
	gorm.io/driver/postgres v1.0.0
	gorm.io/driver/sqlserver v1.0.4
	gorm.io/gorm v1.20.6
	inet.af/netaddr v0.0.0-20211027220019-c74959edd3b6
	k8s.io/apimachinery v0.17.0
	k8s.io/client-go v0.17.0
)
