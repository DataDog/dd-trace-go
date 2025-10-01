// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package instrumentation

import (
	"fmt"
	"strings"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
)

type Package string

const (
	Package99DesignsGQLGen      Package = "99designs/gqlgen"
	PackageAWSSDKGo             Package = "aws/aws-sdk-go"
	PackageAWSSDKGoV2           Package = "aws/aws-sdk-go-v2"
	PackageAWSDatadogLambdaGo   Package = "aws/datadog-lambda-go"
	PackageBradfitzGoMemcache   Package = "bradfitz/gomemcache"
	PackageGCPPubsub            Package = "cloud.google.com/go/pubsub.v1"
	PackageGCPPubsubV2          Package = "cloud.google.com/go/pubsub.v2"
	PackageConfluentKafkaGo     Package = "confluentinc/confluent-kafka-go/kafka"
	PackageConfluentKafkaGoV2   Package = "confluentinc/confluent-kafka-go/kafka.v2"
	PackageDatabaseSQL          Package = "database/sql"
	PackageDimfeldHTTPTreeMuxV5 Package = "dimfeld/httptreemux.v5"
	PackageGoElasticSearchV6    Package = "elastic/go-elasticsearch.v6"
	PackageEmickleiGoRestfulV3  Package = "emicklei/go-restful.v3"
	PackageGin                  Package = "gin-gonic/gin"
	PackageGlobalsignMgo        Package = "globalsign/mgo"
	PackageMongoDriver          Package = "go.mongodb.org/mongo-driver"
	PackageMongoDriverV2        Package = "go.mongodb.org/mongo-driver.v2"
	PackageChi                  Package = "go-chi/chi"
	PackageChiV5                Package = "go-chi/chi.v5"
	PackageGoPGV10              Package = "go-pg/pg.v10"
	PackageGoRedis              Package = "go-redis/redis"
	PackageGoRedisV7            Package = "go-redis/redis.v7"
	PackageGoRedisV8            Package = "go-redis/redis.v8"
	PackageGoCQL                Package = "gocql/gocql"
	PackageGoFiberV2            Package = "gofiber/fiber.v2"
	PackageRedigo               Package = "gomodule/redigo"
	PackageGoogleAPI            Package = "google.golang.org/api"
	PackageGRPC                 Package = "google.golang.org/grpc"

	PackageNetHTTP   Package = "net/http"
	PackageIBMSarama Package = "IBM/sarama"

	PackageValyalaFastHTTP         Package = "valyala/fasthttp"
	PackageUrfaveNegroni           Package = "urfave/negroni"
	PackageTwitchTVTwirp           Package = "twitchtv/twirp"
	PackageTidwallBuntDB           Package = "tidwall/buntdb"
	PackageSyndtrGoLevelDB         Package = "syndtr/goleveldb"
	PackageSirupsenLogrus          Package = "sirupsen/logrus"
	PackageShopifySarama           Package = "Shopify/sarama"
	PackageSegmentioKafkaGo        Package = "segmentio/kafka-go"
	PackageRedisGoRedisV9          Package = "redis/go-redis.v9"
	PackageOlivereElasticV5        Package = "olivere/elastic.v5"
	PackageMiekgDNS                Package = "miekg/dns"
	PackageLabstackEchoV4          Package = "labstack/echo.v4"
	PackageK8SClientGo             Package = "k8s.io/client-go"
	PackageK8SGatewayAPI           Package = "k8s.io/gateway-api"
	PackageJulienschmidtHTTPRouter Package = "julienschmidt/httprouter"
	PackageJmoironSQLx             Package = "jmoiron/sqlx"
	PackageJackcPGXV5              Package = "jackc/pgx.v5"
	PackageHashicorpConsulAPI      Package = "hashicorp/consul"
	PackageHashicorpVaultAPI       Package = "hashicorp/vault"
	PackageGraphQLGoGraphQL        Package = "graphql-go/graphql"
	PackageGraphGophersGraphQLGo   Package = "graph-gophers/graphql-go"
	PackageGormIOGormV1            Package = "gorm.io/gorm.v1"
	PackageGorillaMux              Package = "gorilla/mux"
	PackageUptraceBun              Package = "uptrace/bun"
	PackageLogSlog                 Package = "log/slog"

	PackageValkeyIoValkeyGo               Package = "valkey-io/valkey-go"
	PackageEnvoyProxyGoControlPlane       Package = "envoyproxy/go-control-plane"
	PackageHAProxyStreamProcessingOffload Package = "haproxy/stream-processing-offload"
	PackageOS                             Package = "os"
	PackageRedisRueidis                   Package = "redis/rueidis"
)

// These packages have been removed in v2, but they are kept here for the transitional version.
const (
	PackageEmickleiGoRestful Package = "emicklei/go-restful"
	PackageGaryburdRedigo    Package = "garyburd/redigo"
	PackageGopkgJinZhuGormV1 Package = "gopkg.in/jinzhu/gorm.v1"
	PackageGojiV1Web         Package = "zenazn/goji.v1/web"
	PackageJinzhuGorm        Package = "jinzhu/gorm"
	PackageLabstackEcho      Package = "labstack/echo"
)

type Component int

const (
	ComponentDefault Component = iota
	ComponentServer
	ComponentClient
	ComponentProducer
	ComponentConsumer
)

type componentNames struct {
	useDDServiceV0     bool
	buildServiceNameV0 func(opCtx OperationContext) string
	buildOpNameV0      func(opCtx OperationContext) string
	buildOpNameV1      func(opCtx OperationContext) string
}

type PackageInfo struct {
	external bool

	TracedPackage string
	IsStdLib      bool
	EnvVarPrefix  string

	naming map[Component]componentNames
}

var packages = map[Package]PackageInfo{
	Package99DesignsGQLGen: {
		TracedPackage: "github.com/99designs/gqlgen",
		EnvVarPrefix:  "GQLGEN",
		naming: map[Component]componentNames{
			ComponentDefault: {
				buildServiceNameV0: staticName("graphql"),
				buildOpNameV0: func(opCtx OperationContext) string {
					name := "graphql.request"
					if graphqlOp, ok := opCtx["graphql.operation"]; ok {
						name = fmt.Sprintf("%s.%s", ext.SpanTypeGraphQL, graphqlOp)
					}
					return name
				},
				buildOpNameV1: staticName("graphql.server.request"),
			},
		},
	},
	PackageAWSSDKGo: {
		TracedPackage: "github.com/aws/aws-sdk-go",
		EnvVarPrefix:  "AWS",
		naming: map[Component]componentNames{
			ComponentDefault: {
				buildServiceNameV0: awsBuildDefaultServiceNameV0,
				buildOpNameV0: func(opCtx OperationContext) string {
					awsService, ok := opCtx[ext.AWSService]
					if !ok {
						return ""
					}
					return awsService + ".command"
				},
				buildOpNameV1: awsBuildOpNameV1,
			},
		},
	},
	PackageAWSSDKGoV2: {
		TracedPackage: "github.com/aws/aws-sdk-go-v2",
		EnvVarPrefix:  "AWS",
		naming: map[Component]componentNames{
			ComponentDefault: {
				buildServiceNameV0: awsBuildDefaultServiceNameV0,
				buildOpNameV0: func(opCtx OperationContext) string {
					awsService, ok := opCtx[ext.AWSService]
					if !ok {
						return ""
					}
					return awsService + ".request"
				},
				buildOpNameV1: awsBuildOpNameV1,
			},
		},
	},
	PackageAWSDatadogLambdaGo: {
		TracedPackage: "github.com/DataDog/dd-trace-go/contrib/aws/datadog-lambda-go",
		EnvVarPrefix:  "LAMBDA",
		naming: map[Component]componentNames{
			ComponentDefault: {
				useDDServiceV0:     false,
				buildServiceNameV0: staticName("aws.lambda"),
				buildOpNameV0:      staticName("aws.lambda"),
				buildOpNameV1:      staticName("aws.lambda"),
			},
		},
	},
	PackageBradfitzGoMemcache: {
		TracedPackage: "github.com/bradfitz/gomemcache",
		EnvVarPrefix:  "MEMCACHE",
		naming: map[Component]componentNames{
			ComponentDefault: {
				useDDServiceV0:     false,
				buildServiceNameV0: staticName("memcached"),
				buildOpNameV0:      staticName("memcached.query"),
				buildOpNameV1:      staticName("memcached.command"),
			},
		},
	},
	PackageGCPPubsub: {
		TracedPackage: "cloud.google.com/go/pubsub",
		EnvVarPrefix:  "GCP_PUBSUB",
		naming: map[Component]componentNames{
			ComponentConsumer: {
				useDDServiceV0:     false,
				buildServiceNameV0: staticName(""),
				buildOpNameV0:      staticName("pubsub.receive"),
				buildOpNameV1:      staticName("gcp.pubsub.process"),
			},
			ComponentProducer: {
				useDDServiceV0:     false,
				buildServiceNameV0: staticName(""),
				buildOpNameV0:      staticName("pubsub.publish"),
				buildOpNameV1:      staticName("gcp.pubsub.send"),
			},
		},
	},
	PackageGCPPubsubV2: {
		TracedPackage: "cloud.google.com/go/pubsub/v2",
		EnvVarPrefix:  "GCP_PUBSUB",
		naming: map[Component]componentNames{
			ComponentConsumer: {
				useDDServiceV0:     false,
				buildServiceNameV0: staticName(""),
				buildOpNameV0:      staticName("pubsub.receive"),
				buildOpNameV1:      staticName("gcp.pubsub.process"),
			},
			ComponentProducer: {
				useDDServiceV0:     false,
				buildServiceNameV0: staticName(""),
				buildOpNameV0:      staticName("pubsub.publish"),
				buildOpNameV1:      staticName("gcp.pubsub.send"),
			},
		},
	},
	PackageConfluentKafkaGo: {
		TracedPackage: "github.com/confluentinc/confluent-kafka-go",
		EnvVarPrefix:  "KAFKA",
		naming: map[Component]componentNames{
			ComponentConsumer: {
				useDDServiceV0:     true,
				buildServiceNameV0: staticName("kafka"),
				buildOpNameV0:      staticName("kafka.consume"),
				buildOpNameV1:      staticName("kafka.process"),
			},
			ComponentProducer: {
				useDDServiceV0:     false,
				buildServiceNameV0: staticName("kafka"),
				buildOpNameV0:      staticName("kafka.produce"),
				buildOpNameV1:      staticName("kafka.send"),
			},
		},
	},
	PackageConfluentKafkaGoV2: {
		TracedPackage: "github.com/confluentinc/confluent-kafka-go/v2",
		EnvVarPrefix:  "KAFKA",
		naming: map[Component]componentNames{
			ComponentConsumer: {
				useDDServiceV0:     true,
				buildServiceNameV0: staticName("kafka"),
				buildOpNameV0:      staticName("kafka.consume"),
				buildOpNameV1:      staticName("kafka.process"),
			},
			ComponentProducer: {
				useDDServiceV0:     false,
				buildServiceNameV0: staticName("kafka"),
				buildOpNameV0:      staticName("kafka.produce"),
				buildOpNameV1:      staticName("kafka.send"),
			},
		},
	},
	PackageDatabaseSQL: {
		TracedPackage: "database/sql",
		IsStdLib:      true,
		EnvVarPrefix:  "SQL",
		naming: map[Component]componentNames{
			ComponentDefault: {
				useDDServiceV0: false,
				buildServiceNameV0: func(opCtx OperationContext) string {
					if svc := opCtx["registerService"]; svc != "" {
						return svc
					}
					return fmt.Sprintf("%s.db", opCtx["driverName"])
				},
				buildOpNameV0: func(opCtx OperationContext) string {
					return fmt.Sprintf("%s.query", opCtx["driverName"])
				},
				buildOpNameV1: func(opCtx OperationContext) string {
					return fmt.Sprintf("%s.query", opCtx[ext.DBSystem])
				},
			},
		},
	},
	PackageDimfeldHTTPTreeMuxV5: {
		TracedPackage: "github.com/dimfeld/httptreemux/v5",
		EnvVarPrefix:  "HTTPTREEMUX",
		naming: map[Component]componentNames{
			ComponentServer: {
				useDDServiceV0:     true,
				buildServiceNameV0: staticName("http.router"),
				buildOpNameV0:      staticName("http.request"),
				buildOpNameV1:      staticName("http.server.request"),
			},
		},
	},
	PackageGoElasticSearchV6: {
		TracedPackage: "github.com/elastic/go-elasticsearch/v6",
		EnvVarPrefix:  "ELASTIC",
		naming: map[Component]componentNames{
			ComponentDefault: {
				useDDServiceV0:     false,
				buildServiceNameV0: staticName("elastic.client"),
				buildOpNameV0:      staticName("elasticsearch.query"),
				buildOpNameV1:      staticName("elasticsearch.query"),
			},
		},
	},
	PackageEmickleiGoRestfulV3: {
		TracedPackage: "github.com/emicklei/go-restful/v3",
		EnvVarPrefix:  "RESTFUL",
		naming: map[Component]componentNames{
			ComponentServer: {
				useDDServiceV0:     false,
				buildServiceNameV0: staticName("go-restful"),
				buildOpNameV0:      staticName("http.request"),
				buildOpNameV1:      staticName("http.server.request"),
			},
		},
	},
	PackageGin: {
		TracedPackage: "github.com/gin-gonic/gin",
		EnvVarPrefix:  "GIN",
		naming: map[Component]componentNames{
			ComponentServer: {
				useDDServiceV0:     true,
				buildServiceNameV0: staticName("gin.router"),
				buildOpNameV0:      staticName("http.request"),
				buildOpNameV1:      staticName("http.server.request"),
			},
		},
	},
	PackageGlobalsignMgo: {
		TracedPackage: "github.com/globalsign/mgo",
		EnvVarPrefix:  "MGO",
		naming: map[Component]componentNames{
			ComponentDefault: {
				useDDServiceV0:     false,
				buildServiceNameV0: staticName("mongodb"),
				buildOpNameV0:      staticName("mongodb.query"),
				buildOpNameV1:      staticName("mongodb.query"),
			},
		},
	},
	PackageMongoDriver: {
		TracedPackage: "go.mongodb.org/mongo-driver",
		EnvVarPrefix:  "MONGO",
		naming: map[Component]componentNames{
			ComponentDefault: {
				useDDServiceV0:     false,
				buildServiceNameV0: staticName("mongo"),
				buildOpNameV0:      staticName("mongodb.query"),
				buildOpNameV1:      staticName("mongodb.query"),
			},
		},
	},
	PackageMongoDriverV2: {
		TracedPackage: "go.mongodb.org/mongo-driver/v2",
		EnvVarPrefix:  "MONGO",
		naming: map[Component]componentNames{
			ComponentDefault: {
				useDDServiceV0:     false,
				buildServiceNameV0: staticName("mongo"),
				buildOpNameV0:      staticName("mongodb.query"),
				buildOpNameV1:      staticName("mongodb.query"),
			},
		},
	},
	PackageChi: {
		TracedPackage: "github.com/go-chi/chi",
		EnvVarPrefix:  "CHI",
		naming: map[Component]componentNames{
			ComponentServer: {
				useDDServiceV0:     true,
				buildServiceNameV0: staticName("chi.router"),
				buildOpNameV0:      staticName("http.request"),
				buildOpNameV1:      staticName("http.server.request"),
			},
		},
	},
	PackageChiV5: {
		TracedPackage: "github.com/go-chi/chi/v5",
		EnvVarPrefix:  "CHI",
		naming: map[Component]componentNames{
			ComponentServer: {
				useDDServiceV0:     true,
				buildServiceNameV0: staticName("chi.router"),
				buildOpNameV0:      staticName("http.request"),
				buildOpNameV1:      staticName("http.server.request"),
			},
		},
	},
	PackageGoPGV10: {
		TracedPackage: "github.com/go-pg/pg/v10",
		EnvVarPrefix:  "GOPG",
		naming: map[Component]componentNames{
			ComponentDefault: {
				useDDServiceV0:     true,
				buildServiceNameV0: staticName("gopg.db"),
				buildOpNameV0:      staticName("go-pg"),
				buildOpNameV1:      staticName("postgresql.query"),
			},
		},
	},
	PackageGoRedis: {
		TracedPackage: "github.com/go-redis/redis",
		EnvVarPrefix:  "REDIS",
		naming: map[Component]componentNames{
			ComponentDefault: {
				useDDServiceV0:     false,
				buildServiceNameV0: staticName("redis.client"),
				buildOpNameV0:      staticName("redis.command"),
				buildOpNameV1:      staticName("redis.command"),
			},
		},
	},
	PackageGoRedisV7: {
		TracedPackage: "github.com/go-redis/redis/v7",
		EnvVarPrefix:  "REDIS",
		naming: map[Component]componentNames{
			ComponentDefault: {
				useDDServiceV0:     false,
				buildServiceNameV0: staticName("redis.client"),
				buildOpNameV0:      staticName("redis.command"),
				buildOpNameV1:      staticName("redis.command"),
			},
		},
	},
	PackageGoRedisV8: {
		TracedPackage: "github.com/go-redis/redis/v8",
		EnvVarPrefix:  "REDIS",
		naming: map[Component]componentNames{
			ComponentDefault: {
				useDDServiceV0:     false,
				buildServiceNameV0: staticName("redis.client"),
				buildOpNameV0:      staticName("redis.command"),
				buildOpNameV1:      staticName("redis.command"),
			},
		},
	},
	PackageGoCQL: {
		TracedPackage: "github.com/gocql/gocql",
		EnvVarPrefix:  "GOCQL",
		naming: map[Component]componentNames{
			ComponentDefault: {
				useDDServiceV0:     false,
				buildServiceNameV0: staticName("gocql.query"),
				buildOpNameV0: func(opCtx OperationContext) string {
					if opCtx["operationType"] == "batch" {
						return "cassandra.batch"
					}
					return "cassandra.query"
				},
				buildOpNameV1: staticName("cassandra.query"),
			},
		},
	},
	PackageGoFiberV2: {
		TracedPackage: "github.com/gofiber/fiber/v2",
		EnvVarPrefix:  "FIBER",
		naming: map[Component]componentNames{
			ComponentServer: {
				useDDServiceV0:     true,
				buildServiceNameV0: staticName("fiber"),
				buildOpNameV0:      staticName("http.request"),
				buildOpNameV1:      staticName("http.server.request"),
			},
		},
	},
	PackageRedigo: {
		TracedPackage: "github.com/gomodule/redigo",
		EnvVarPrefix:  "REDIGO",
		naming: map[Component]componentNames{
			ComponentDefault: {
				useDDServiceV0:     false,
				buildServiceNameV0: staticName("redis.conn"),
				buildOpNameV0:      staticName("redis.command"),
				buildOpNameV1:      staticName("redis.command"),
			},
		},
	},
	PackageGoogleAPI: {
		TracedPackage: "google.golang.org/api",
		EnvVarPrefix:  "GOOGLE_API",
		naming:        nil, // this package does not use naming schema
	},
	PackageGRPC: {
		TracedPackage: "google.golang.org/grpc",
		EnvVarPrefix:  "GRPC",
		naming: map[Component]componentNames{
			ComponentServer: {
				useDDServiceV0:     true,
				buildServiceNameV0: staticName("grpc.server"),
				buildOpNameV0:      staticName("grpc.server"),
				buildOpNameV1:      staticName("grpc.server.request"),
			},
			ComponentClient: {
				useDDServiceV0:     false,
				buildServiceNameV0: staticName("grpc.client"),
				buildOpNameV0:      staticName("grpc.client"),
				buildOpNameV1:      staticName("grpc.client.request"),
			},
		},
	},
	// TODO

	PackageNetHTTP: {
		TracedPackage: "net/http",
		IsStdLib:      true,
		EnvVarPrefix:  "HTTP",
		naming: map[Component]componentNames{
			ComponentServer: {
				useDDServiceV0:     true,
				buildServiceNameV0: staticName("http.router"),
				buildOpNameV0:      staticName("http.request"),
				buildOpNameV1:      staticName("http.server.request"),
			},
			ComponentClient: {
				useDDServiceV0:     false,
				buildServiceNameV0: staticName(""),
				buildOpNameV0:      staticName("http.request"),
				buildOpNameV1:      staticName("http.client.request"),
			},
		},
	},
	PackageValyalaFastHTTP: {
		TracedPackage: "github.com/valyala/fasthttp",
		EnvVarPrefix:  "FASTHTTP",
		naming: map[Component]componentNames{
			ComponentServer: {
				useDDServiceV0:     false,
				buildServiceNameV0: staticName("fasthttp"),
				buildOpNameV0:      staticName("http.request"),
				buildOpNameV1:      staticName("http.server.request"),
			},
		},
	},
	PackageUrfaveNegroni: {
		TracedPackage: "github.com/urfave/negroni",
		EnvVarPrefix:  "NEGRONI",
		naming: map[Component]componentNames{
			ComponentServer: {
				useDDServiceV0:     true,
				buildServiceNameV0: staticName("negroni.router"),
				buildOpNameV0:      staticName("http.request"),
				buildOpNameV1:      staticName("http.server.request"),
			},
		},
	},
	PackageTwitchTVTwirp: {
		TracedPackage: "github.com/twitchtv/twirp",
		EnvVarPrefix:  "TWIRP",
		naming: map[Component]componentNames{
			ComponentServer: {
				useDDServiceV0:     true,
				buildServiceNameV0: staticName("twirp-server"),
				buildOpNameV0: func(opCtx OperationContext) string {
					rpcService, ok := opCtx[ext.RPCService]
					if rpcService == "" || !ok {
						return "twirp.service"
					}
					return fmt.Sprintf("twirp.%s", rpcService)
				},
				buildOpNameV1: staticName("twirp.server.request"),
			},
			ComponentClient: {
				useDDServiceV0:     true,
				buildServiceNameV0: staticName("twirp-client"),
				buildOpNameV0:      staticName("twirp.request"),
				buildOpNameV1:      staticName("twirp.client.request"),
			},
		},
	},
	PackageTidwallBuntDB: {
		TracedPackage: "github.com/tidwall/buntdb",
		EnvVarPrefix:  "BUNTDB",
		naming: map[Component]componentNames{
			ComponentDefault: {
				useDDServiceV0:     false,
				buildServiceNameV0: staticName("buntdb"),
				buildOpNameV0:      staticName("buntdb.query"),
				buildOpNameV1:      staticName("buntdb.query"),
			},
		},
	},
	PackageSyndtrGoLevelDB: {
		TracedPackage: "github.com/syndtr/goleveldb",
		EnvVarPrefix:  "LEVELDB",
		naming: map[Component]componentNames{
			ComponentDefault: {
				useDDServiceV0:     false,
				buildServiceNameV0: staticName("leveldb"),
				buildOpNameV0:      staticName("leveldb.query"),
				buildOpNameV1:      staticName("leveldb.query"),
			},
		},
	},
	PackageSirupsenLogrus: {
		TracedPackage: "github.com/sirupsen/logrus",
		EnvVarPrefix:  "LOGRUS",
	},
	PackageShopifySarama: {
		TracedPackage: "github.com/Shopify/sarama",
		EnvVarPrefix:  "SARAMA",
		naming: map[Component]componentNames{
			ComponentConsumer: {
				useDDServiceV0:     true,
				buildServiceNameV0: staticName("kafka"),
				buildOpNameV0:      staticName("kafka.consume"),
				buildOpNameV1:      staticName("kafka.process"),
			},
			ComponentProducer: {
				useDDServiceV0:     false,
				buildServiceNameV0: staticName("kafka"),
				buildOpNameV0:      staticName("kafka.produce"),
				buildOpNameV1:      staticName("kafka.send"),
			},
		},
	},
	PackageSegmentioKafkaGo: {
		TracedPackage: "github.com/segmentio/kafka-go",
		EnvVarPrefix:  "KAFKA",
		naming: map[Component]componentNames{
			ComponentConsumer: {
				useDDServiceV0:     true,
				buildServiceNameV0: staticName("kafka"),
				buildOpNameV0:      staticName("kafka.consume"),
				buildOpNameV1:      staticName("kafka.process"),
			},
			ComponentProducer: {
				useDDServiceV0:     false,
				buildServiceNameV0: staticName("kafka"),
				buildOpNameV0:      staticName("kafka.produce"),
				buildOpNameV1:      staticName("kafka.send"),
			},
		},
	},
	PackageRedisGoRedisV9: {
		TracedPackage: "github.com/redis/go-redis/v9",
		EnvVarPrefix:  "REDIS",
		naming: map[Component]componentNames{
			ComponentDefault: {
				useDDServiceV0:     true,
				buildServiceNameV0: staticName("redis.client"),
				buildOpNameV0:      staticName("redis.command"),
				buildOpNameV1:      staticName("redis.command"),
			},
		},
	},
	PackageRedisRueidis: {
		TracedPackage: "github.com/redis/rueidis",
		EnvVarPrefix:  "REDIS",
		naming: map[Component]componentNames{
			ComponentDefault: {
				useDDServiceV0:     true,
				buildServiceNameV0: staticName("redis.client"),
				buildOpNameV0:      staticName("redis.command"),
				buildOpNameV1:      staticName("redis.command"),
			},
		},
	},
	PackageOlivereElasticV5: {
		TracedPackage: "gopkg.in/olivere/elastic.v5",
		EnvVarPrefix:  "ELASTIC",
		naming: map[Component]componentNames{
			ComponentDefault: {
				useDDServiceV0:     false,
				buildServiceNameV0: staticName("elastic.client"),
				buildOpNameV0:      staticName("elasticsearch.query"),
				buildOpNameV1:      staticName("elasticsearch.query"),
			},
		},
	},
	PackageMiekgDNS: {
		TracedPackage: "github.com/miekg/dns",
	},
	PackageLabstackEchoV4: {
		TracedPackage: "github.com/labstack/echo/v4",
		EnvVarPrefix:  "ECHO",
		naming: map[Component]componentNames{
			ComponentServer: {
				useDDServiceV0:     true,
				buildServiceNameV0: staticName("echo"),
				buildOpNameV0:      staticName("http.request"),
				buildOpNameV1:      staticName("http.server.request"),
			},
		},
	},
	PackageK8SClientGo: {
		TracedPackage: "k8s.io/client-go",
	},
	PackageK8SGatewayAPI: {
		TracedPackage: "sigs.k8s.io/gateway-api",
	},
	PackageJulienschmidtHTTPRouter: {
		TracedPackage: "github.com/julienschmidt/httprouter",
		EnvVarPrefix:  "HTTPROUTER",
		naming: map[Component]componentNames{
			ComponentServer: {
				useDDServiceV0:     true,
				buildServiceNameV0: staticName("http.router"),
				buildOpNameV0:      staticName("http.request"),
				buildOpNameV1:      staticName("http.server.request"),
			},
		},
	},
	PackageJmoironSQLx: {
		TracedPackage: "github.com/jmoiron/sqlx",
	},
	PackageJackcPGXV5: {
		TracedPackage: "github.com/jackc/pgx/v5",
		naming: map[Component]componentNames{
			ComponentDefault: {
				useDDServiceV0:     false,
				buildServiceNameV0: staticName("postgres.db"),
			},
		},
	},
	PackageIBMSarama: {
		TracedPackage: "github.com/IBM/sarama",
		EnvVarPrefix:  "SARAMA",
		naming: map[Component]componentNames{
			ComponentConsumer: {
				useDDServiceV0:     true,
				buildServiceNameV0: staticName("kafka"),
				buildOpNameV0:      staticName("kafka.consume"),
				buildOpNameV1:      staticName("kafka.process"),
			},
			ComponentProducer: {
				useDDServiceV0:     false,
				buildServiceNameV0: staticName("kafka"),
				buildOpNameV0:      staticName("kafka.produce"),
				buildOpNameV1:      staticName("kafka.send"),
			},
		},
	},
	PackageHashicorpConsulAPI: {
		TracedPackage: "github.com/hashicorp/consul/api",
		EnvVarPrefix:  "CONSUL",
		naming: map[Component]componentNames{
			ComponentDefault: {
				useDDServiceV0:     false,
				buildServiceNameV0: staticName("consul"),
				buildOpNameV0:      staticName("consul.command"),
				buildOpNameV1:      staticName("consul.query"),
			},
		},
	},
	PackageHashicorpVaultAPI: {
		TracedPackage: "github.com/hashicorp/vault/api",
		EnvVarPrefix:  "VAULT",
		naming: map[Component]componentNames{
			ComponentDefault: {
				useDDServiceV0:     false,
				buildServiceNameV0: staticName("vault"),
				buildOpNameV0:      staticName("http.request"),
				buildOpNameV1:      staticName("vault.query"),
			},
		},
	},
	PackageGraphQLGoGraphQL: {
		TracedPackage: "github.com/graphql-go/graphql",
		EnvVarPrefix:  "GRAPHQL",
		naming: map[Component]componentNames{
			ComponentDefault: {
				useDDServiceV0:     true,
				buildServiceNameV0: staticName("graphql.server"),
			},
		},
	},
	PackageGraphGophersGraphQLGo: {
		TracedPackage: "github.com/graph-gophers/graphql-go",
		EnvVarPrefix:  "GRAPHQL",
		naming: map[Component]componentNames{
			ComponentDefault: {
				useDDServiceV0:     true,
				buildServiceNameV0: staticName("graphql.server"),
				buildOpNameV0:      staticName("graphql.request"),
				buildOpNameV1:      staticName("graphql.server.request"),
			},
		},
	},
	PackageGormIOGormV1: {
		TracedPackage: "gorm.io/gorm",
	},
	PackageGorillaMux: {
		TracedPackage: "github.com/gorilla/mux",
		EnvVarPrefix:  "MUX",
		naming: map[Component]componentNames{
			ComponentServer: {
				useDDServiceV0:     true,
				buildServiceNameV0: staticName("mux.router"),
				buildOpNameV0:      staticName("http.request"),
				buildOpNameV1:      staticName("http.server.request"),
			},
		},
	},
	PackageUptraceBun: {
		TracedPackage: "github.com/uptrace/bun",
		naming: map[Component]componentNames{
			ComponentDefault: {
				useDDServiceV0:     true,
				buildServiceNameV0: staticName("bun.db"),
			},
		},
	},
	PackageLogSlog: {
		TracedPackage: "log/slog",
		IsStdLib:      true,
	},
	PackageValkeyIoValkeyGo: {
		TracedPackage: "github.com/valkey-io/valkey-go",
		EnvVarPrefix:  "VALKEY",
		naming: map[Component]componentNames{
			ComponentDefault: {
				useDDServiceV0:     true,
				buildServiceNameV0: staticName("valkey.client"),
			},
		},
	},
	PackageEnvoyProxyGoControlPlane: {
		TracedPackage: "github.com/envoyproxy/go-control-plane",
	},
	PackageHAProxyStreamProcessingOffload: {
		TracedPackage: "haproxy/stream-processing-offload",
	},
	PackageOS: {
		TracedPackage: "os",
	},
	PackageEmickleiGoRestful: {
		TracedPackage: "github.com/emicklei/go-restful",
		EnvVarPrefix:  "RESTFUL",
		naming: map[Component]componentNames{
			ComponentServer: {
				useDDServiceV0:     false,
				buildServiceNameV0: staticName("go-restful"),
				buildOpNameV0:      staticName("http.request"),
				buildOpNameV1:      staticName("http.server.request"),
			},
		},
	},
	PackageGaryburdRedigo: {
		TracedPackage: "github.com/garyburd/redigo",
		EnvVarPrefix:  "REDIGO",
		naming: map[Component]componentNames{
			ComponentDefault: {
				useDDServiceV0:     false,
				buildServiceNameV0: staticName("redis.conn"),
				buildOpNameV0:      staticName("redis.command"),
				buildOpNameV1:      staticName("redis.command"),
			},
		},
	},
	PackageGopkgJinZhuGormV1: {
		TracedPackage: "gopkg.in/jinzhu/gorm.v1",
	},
	PackageGojiV1Web: {
		TracedPackage: "github.com/zenazn/goji/web",
		EnvVarPrefix:  "GOJI",
		naming: map[Component]componentNames{
			ComponentServer: {
				useDDServiceV0:     true,
				buildServiceNameV0: staticName("http.router"),
				buildOpNameV0:      staticName("http.request"),
				buildOpNameV1:      staticName("http.server.request"),
			},
		},
	},
	PackageJinzhuGorm: {
		TracedPackage: "github.com/jinzhu/gorm",
	},
	PackageLabstackEcho: {
		TracedPackage: "github.com/labstack/echo",
		EnvVarPrefix:  "ECHO",
		naming: map[Component]componentNames{
			ComponentServer: {
				useDDServiceV0:     true,
				buildServiceNameV0: staticName("echo"),
				buildOpNameV0:      staticName("http.request"),
				buildOpNameV1:      staticName("http.server.request"),
			},
		},
	},
}

func staticName(name string) func(OperationContext) string {
	return func(_ OperationContext) string {
		return name
	}
}

func awsBuildDefaultServiceNameV0(opCtx OperationContext) string {
	awsService, ok := opCtx[ext.AWSService]
	if !ok {
		return ""
	}
	return "aws." + awsService
}

func awsBuildOpNameV1(opCtx OperationContext) string {
	awsService, ok := opCtx[ext.AWSService]
	if !ok {
		return ""
	}
	awsOp, ok := opCtx[ext.AWSOperation]
	if !ok {
		return ""
	}
	op := "request"
	if isAWSMessagingSendOp(awsService, awsOp) {
		op = "send"
	}
	return fmt.Sprintf("aws.%s.%s", strings.ToLower(awsService), op)
}

func isAWSMessagingSendOp(awsService, awsOperation string) bool {
	s, op := strings.ToLower(awsService), strings.ToLower(awsOperation)
	if s == "sqs" {
		return strings.HasPrefix(op, "sendmessage")
	}
	if s == "sns" {
		return op == "publish"
	}
	return false
}

// GetPackages returns a map of Package to the corresponding instrumented module.
func GetPackages() map[Package]PackageInfo {
	cp := make(map[Package]PackageInfo)
	for pkg, info := range packages {
		cp[pkg] = info
	}
	return cp
}
