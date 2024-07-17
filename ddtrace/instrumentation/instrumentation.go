package instrumentation

import (
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/globalconfig"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

func Load(pkg Package) {
	info, ok := packages[pkg]
	if !ok {
		panic("instrumentation package: " + pkg + "was not found. If this is an external package, you must" +
			"call instrumentation.Register first")
	}

	telemetry.LoadIntegration(string(pkg))
	tracer.MarkIntegrationImported(info.TracedPackage)
}

func DefaultAnalyticsRate() float64 {
	return globalconfig.AnalyticsRate()
}

func DefaultServiceName(pkg Package, operation string, opCtx OperationContext) (svc string) {
	sv := getVersion()
	ddService := globalconfig.ServiceName()
	svc = ddService

	if (sv == namingSchemaV1 || removeFakeServiceNames) && ddService != "" {
		return
	}
	n, ok := moduleNamings[pkg]
	if !ok {
		return
	}
	op, ok := n.operations[operation]
	if !ok {
		return
	}
	if op.buildDefaultServiceNameV0 == nil {
		return
	}
	return op.buildDefaultServiceNameV0(opCtx)
}

func OperationName(pkg Package, operation string, opCtx OperationContext) string {
	n, ok := moduleNamings[pkg]
	if !ok {
		return ""
	}
	op, ok := n.operations[operation]
	if !ok {
		return ""
	}

	sv := getVersion()
	switch sv {
	case namingSchemaV1:
		return op.buildNameV1(opCtx)
	default:
		return op.buildNameV0(opCtx)
	}
}
