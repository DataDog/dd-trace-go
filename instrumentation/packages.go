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
	PackageBradfitzGoMemcache   Package = "bradfitz/gomemcache"
	PackageCloudGoogleComPubsub Package = "cloud.google.com/go/pubsub.v1"
	PackageConfluentKafkaGo     Package = "confluentinc/confluent-kafka-go/kafka"
	PackageConfluentKafkaGoV2   Package = "confluentinc/confluent-kafka-go/kafka.v2"

	// TODO: ...

	PackageNetHTTP   Package = "net/http"
	PackageIBMSarama Package = "IBM/sarama"

	PackageValyalaFastHTTP Package = "valyala/fasthttp"
	PackageUrfaveNegroni   Package = "urfave/negroni"
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
		TracedPackage: "",
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
	PackageNetHTTP: {
		external:      false,
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

//
//func opV1(t IntegrationType) string {
//	switch t {
//	// Client/Server
//	case HTTPClient:
//		return "http.client.request"
//	case HTTPServer:
//		return "http.server.request"
//	case GRPCClient:
//		return "grpc.client.request"
//	case GRPCServer:
//		return "grpc.server.request"
//	case GraphqlServer:
//		return "graphql.server.request"
//	case TwirpClient:
//		return "twirp.client.request"
//	case TwirpServer:
//		return "twirp.server.request"
//
//	// Messaging
//	case KafkaOutbound:
//		return "kafka.send"
//	case KafkaInbound:
//		return "kafka.process"
//	case GCPPubSubInbound:
//		return "gcp.pubsub.process"
//	case GCPPubSubOutbound:
//		return "gcp.pubsub.send"
//
//	// Cache
//	case MemcachedOutbound:
//		return "memcached.command"
//	case RedisOutbound:
//		return "redis.command"
//
//	// Database
//	case ElasticSearchOutbound:
//		return "elasticsearch.query"
//	case MongoDBOutbound:
//		return "mongodb.query"
//	case CassandraOutbound:
//		return "cassandra.query"
//	case LevelDBOutbound:
//		return "leveldb.query"
//	case BuntDBOutbound:
//		return "buntdb.query"
//	case ConsulOutbound:
//		return "consul.query"
//	case VaultOutbound:
//		return "vault.query"
//	}
//	return ""
//}
//
//func opV0(t IntegrationType) string {
//	switch t {
//	case HTTPClient, HTTPServer:
//		return "http.request"
//	case GRPCClient:
//		return "grpc.client"
//	case GRPCServer:
//		return "grpc.server"
//	case GraphqlServer:
//		return "graphql.request"
//	case TwirpClient:
//		return "twirp.request"
//	case TwirpServer:
//		return "twirp.request"
//	case KafkaOutbound:
//		return "kafka.produce"
//	case KafkaInbound:
//		return "kafka.consume"
//	case GCPPubSubInbound:
//		return "pubsub.receive"
//	case GCPPubSubOutbound:
//		return "pubsub.publish"
//	case MemcachedOutbound:
//		return "memcached.query"
//	case RedisOutbound:
//		return "redis.command"
//	case ElasticSearchOutbound:
//		return "elasticsearch.query"
//	case MongoDBOutbound:
//		return "mongodb.query"
//	case CassandraOutbound:
//		return "cassandra.query"
//	case LevelDBOutbound:
//		return "leveldb.query"
//	case BuntDBOutbound:
//		return "buntdb.query"
//	case ConsulOutbound:
//		return "consul.command"
//	case VaultOutbound:
//		return "http.request"
//	}
//	return ""
//}
