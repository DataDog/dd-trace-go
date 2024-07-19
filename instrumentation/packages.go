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
	PackageBradfitzMemcache     Package = "bradfitz/gomemcache/memcache"
	PackageCloudGoogleComPubsub Package = "cloud.google.com/go/pubsub.v1"
	PackageConfluentKafkaGo     Package = "confluentinc/confluent-kafka-go/kafka"
	PackageConfluentKafkaGoV2   Package = "confluentinc/confluent-kafka-go/kafka.v2"

	// TODO: ...

	PackageNetHTTP   Package = "net/http"
	PackageIBMSarama Package = "IBM/sarama"
)

type Component int

const (
	ComponentDefault Component = iota
	ComponentServer
	ComponentClient
)

func RegisterPackage(name string, info PackageInfo) error {
	info.external = true
	return nil
}

type componentNames struct {
	buildDefaultServiceNameV0 func(opCtx OperationContext) string
	buildOpNameV0             func(opCtx OperationContext) string
	buildOpNameV1             func(opCtx OperationContext) string
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
				buildDefaultServiceNameV0: staticName("graphql"),
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
				buildDefaultServiceNameV0: awsBuildDefaultServiceNameV0,
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
				buildDefaultServiceNameV0: awsBuildDefaultServiceNameV0,
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

	PackageNetHTTP: {
		external:      false,
		TracedPackage: "net/http",
		EnvVarPrefix:  "HTTP",
		naming: map[Component]componentNames{
			ComponentServer: {
				buildDefaultServiceNameV0: staticName("http.router"),
				buildOpNameV0:             staticName("http.request"),
				buildOpNameV1:             staticName("http.server.request"),
			},
			ComponentClient: {
				buildDefaultServiceNameV0: staticName(""),
				buildOpNameV0:             staticName("http.request"),
				buildOpNameV1:             staticName("http.client.request"),
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
