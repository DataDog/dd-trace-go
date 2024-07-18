package instrumentation

import (
	"fmt"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"strings"
)

type componentNames struct {
	fallbackServiceName       string
	buildDefaultServiceNameV0 func(opCtx OperationContext) string
	buildOpNameV0             func(opCtx OperationContext) string
	buildOpNameV1             func(opCtx OperationContext) string
}

var (
	packageNames = map[Package]map[Component]componentNames{
		Package99DesignsGQLGen: {
			ComponentDefault: {
				fallbackServiceName:       "graphql",
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
		PackageAWSSDKGoV2: {
			ComponentDefault: {
				buildDefaultServiceNameV0: func(opCtx OperationContext) string {
					awsService, ok := opCtx["aws_service"]
					if !ok {
						return ""
					}
					return "aws." + awsService
				},
				buildOpNameV0: func(opCtx OperationContext) string {
					awsService, ok := opCtx["aws_service"]
					if !ok {
						return ""
					}
					return awsService + ".request"
				},
				buildOpNameV1: func(opCtx OperationContext) string {
					awsService, ok := opCtx["aws_service"]
					if !ok {
						return ""
					}
					awsOp, ok := opCtx["aws.operation"]
					if !ok {
						return ""
					}
					op := "request"
					if isAWSMessagingSendOp(awsService, awsOp) {
						op = "send"
					}
					return fmt.Sprintf("aws.%s.%s", strings.ToLower(awsService), op)
				},
			},
		},
		PackageNetHTTP: {
			ComponentServer: {
				buildDefaultServiceNameV0: nil,
				buildOpNameV0:             staticName("http.request"),
				buildOpNameV1:             staticName("http.server.request"),
			},
			ComponentClient: {
				buildDefaultServiceNameV0: staticName(""),
				buildOpNameV0:             staticName("http.request"),
				buildOpNameV1:             staticName("http.client.request"),
			},
		},
		// continue adding here ...
	}
)

func staticName(name string) func(OperationContext) string {
	return func(_ OperationContext) string {
		return name
	}
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
