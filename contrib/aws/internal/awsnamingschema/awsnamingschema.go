package awsnamingschema

import (
	"fmt"
	"strings"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/namingschema"
)

type GetV0SpanNameFunc func(awsService string) string

type opSchema struct {
	awsService   string
	awsOperation string
	getV0        GetV0SpanNameFunc
}

// NewAWSOutboundOp creates a new naming schema for AWS client outbound operations.
func NewAWSOutboundOp(awsService, awsOperation string, getV0 GetV0SpanNameFunc) *namingschema.Schema {
	return namingschema.New(&opSchema{awsService: awsService, awsOperation: awsOperation, getV0: getV0})
}

func (o *opSchema) V0() string {
	return o.getV0(o.awsService)
}

func (o *opSchema) V1() string {
	op := "request"
	if isMessagingSendOp(o.awsService, o.awsOperation) {
		op = "send"
	}
	return fmt.Sprintf("aws.%s.%s", strings.ToLower(o.awsService), op)
}

func isMessagingSendOp(awsService, awsOperation string) bool {
	s, op := strings.ToLower(awsService), strings.ToLower(awsOperation)
	if s == "sqs" {
		return strings.HasPrefix(op, "sendmessage")
	}
	if s == "sns" {
		return op == "publish"
	}
	return false
}
