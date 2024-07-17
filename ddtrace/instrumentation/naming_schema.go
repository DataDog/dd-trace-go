package instrumentation

import (
	"fmt"
	"os"
	"strings"
	"sync/atomic"

	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

type namingSchema int

const (
	// SchemaV0 represents naming schema v0.
	// This constant is not intended for use by external consumers, no API stability is guaranteed.
	namingSchemaV0 = iota
	// SchemaV1 represents naming schema v1.
	// This constant is not intended for use by external consumers, no API stability is guaranteed.
	namingSchemaV1
)

var (
	activeNamingSchema     int32
	removeFakeServiceNames bool
)

// GetVersion returns the global naming schema version used for this application.
func getVersion() namingSchema {
	return namingSchema(atomic.LoadInt32(&activeNamingSchema))
}

// SetVersion sets the global naming schema version used for this application.
func setVersion(v namingSchema) {
	atomic.StoreInt32(&activeNamingSchema, int32(v))
}

func parseVersion(v string) (namingSchema, bool) {
	switch strings.ToLower(v) {
	case "", "v0":
		return namingSchemaV0, true
	case "v1":
		return namingSchemaV1, true
	default:
		return namingSchemaV0, false
	}
}

func init() {
	schemaVersionStr := os.Getenv("DD_TRACE_SPAN_ATTRIBUTE_SCHEMA")
	if v, ok := parseVersion(schemaVersionStr); ok {
		setVersion(v)
	} else {
		setVersion(namingSchemaV0)
		log.Warn("DD_TRACE_SPAN_ATTRIBUTE_SCHEMA=%s is not a valid value, setting to default of v%d", schemaVersionStr, v)
	}
	// Allow DD_TRACE_SPAN_ATTRIBUTE_SCHEMA=v0 users to disable default integration (contrib AKA v0) service names.
	// These default service names are always disabled for v1 onwards.
	removeFakeServiceNames = internal.BoolEnv("DD_TRACE_REMOVE_INTEGRATION_SERVICE_NAMES_ENABLED", false)
}

// OperationContext holds metadata about an instrumentation operation.
type OperationContext map[string]string

// namings holds naming information for an instrumentation.
type namings struct {
	operations map[string]operationNaming
}

type operationNaming struct {
	buildDefaultServiceNameV0 func(opCtx OperationContext) string
	buildNameV0               func(opCtx OperationContext) string
	buildNameV1               func(opCtx OperationContext) string
}

var (
	moduleNamings = map[Package]namings{
		PackageAWSSDKGoV2: {
			operations: map[string]operationNaming{
				"default": {
					buildDefaultServiceNameV0: func(opCtx OperationContext) string {
						awsService, ok := opCtx["aws_service"]
						if !ok {
							return ""
						}
						return "aws." + awsService
					},
					buildNameV0: func(opCtx OperationContext) string {
						awsService, ok := opCtx["aws_service"]
						if !ok {
							return ""
						}
						return awsService + ".request"
					},
					buildNameV1: func(opCtx OperationContext) string {
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
		},
		PackageNetHTTP: {
			operations: map[string]operationNaming{
				"server": {
					buildDefaultServiceNameV0: nil,
					buildNameV0:               staticName("http.request"),
					buildNameV1:               staticName("http.server.request"),
				},
				"client": {
					buildDefaultServiceNameV0: staticName(""),
					buildNameV0:               staticName("http.request"),
					buildNameV1:               staticName("http.client.request"),
				},
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
