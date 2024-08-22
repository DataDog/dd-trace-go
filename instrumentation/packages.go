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
	Package99DesignsGQLGen         Package = "99designs/gqlgen"
	PackageAWSSDKGo                Package = "aws/aws-sdk-go"
	PackageAWSSDKGoV2              Package = "aws/aws-sdk-go-v2"
	PackageBradfitzGoMemcache      Package = "bradfitz/gomemcache"
	PackageCloudGoogleComPubsub    Package = "cloud.google.com/go/pubsub.v1"
	PackageConfluentKafkaGo        Package = "confluentinc/confluent-kafka-go/kafka"
	PackageConfluentKafkaGoV2      Package = "confluentinc/confluent-kafka-go/kafka.v2"
	PackageDatabaseSQL             Package = "database/sql"
	PackageDimfeldHTTPTreeMuxV5    Package = "dimfeld/httptreemux.v5"
	PackageGoElasticSearchV6       Package = "elastic/go-elasticsearch.v6"
	PackageEmickleiGoRestfulV3     Package = "emicklei/go-restful.v3"
	PackageGinGonicGin             Package = "gin-gonic/gin"
	PackageGlobalsignMgo           Package = "globalsign/mgo"
	PackageGoMongoDBOrgMongoDriver Package = "go.mongodb.org/mongo-driver"
	// TODO: ...

	PackageNetHTTP   Package = "net/http"
	PackageIBMSarama Package = "IBM/sarama"

	PackageValyalaFastHTTP Package = "valyala/fasthttp"
	PackageUrfaveNegroni   Package = "urfave/negroni"
	PackageTwitchTVTwirp   Package = "twitchtv/twirp"
	PackageTidwallBuntDB   Package = "tidwall/buntdb"
	PackageSyndtrGoLevelDB Package = "syndtr/goleveldb/leveldb"
	PackageSirupsenLogrus  Package = "sirupsen/logrus"
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
	PackageCloudGoogleComPubsub: {
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
	PackageGinGonicGin: {
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
	PackageGoMongoDBOrgMongoDriver: {
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
	PackageNetHTTP: {
		TracedPackage: "net/http",
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
		TracedPackage: "tidwall/buntdb",
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
		TracedPackage: "syndtr/goleveldb/leveldb",
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
