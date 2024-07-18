package instrumentation

import (
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/internal/namingschema"
	"github.com/DataDog/dd-trace-go/v2/internal/globalconfig"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

// OperationContext holds metadata about an instrumentation operation.
type OperationContext map[string]string

// Load attempts to load the requested package instrumentation. It panics if the package has not been registered.
func Load(pkg Package) *Instrumentation {
	info, ok := packages[pkg]
	if !ok {
		panic("instrumentation package: " + pkg + "was not found. If this is an external package, you must" +
			"call instrumentation.Register first")
	}

	telemetry.LoadIntegration(string(pkg))
	tracer.MarkIntegrationImported(info.TracedPackage)

	return &Instrumentation{
		pkg: pkg,
	}
}

// Instrumentation represents instrumentation for a package.
type Instrumentation struct {
	pkg Package
}

func (i *Instrumentation) DefaultAnalyticsRate() float64 {
	return globalconfig.AnalyticsRate()
}

// DefaultServiceName returns the default service name to be set for the given instrumentation component.
func (i *Instrumentation) DefaultServiceName(component Component, opCtx OperationContext) string {
	cfg := namingschema.GetConfig()

	components, ok := packageNames[i.pkg]
	if !ok {
		return cfg.DDService
	}
	n, ok := components[component]
	if !ok {
		return cfg.DDService
	}

	if cfg.NamingSchemaVersion == namingschema.VersionV1 || cfg.RemoveFakeServiceNames || n.buildDefaultServiceNameV0 == nil {
		if cfg.DDService != "" {
			return cfg.DDService
		}
		return n.fallbackServiceName
	}

	return n.buildDefaultServiceNameV0(opCtx)
}

// OperationName returns the operation name to be set for the given instrumentation component.
func (i *Instrumentation) OperationName(component Component, opCtx OperationContext) string {
	components, ok := packageNames[i.pkg]
	if !ok {
		return ""
	}
	op, ok := components[component]
	if !ok {
		return ""
	}

	cfg := namingschema.GetConfig()
	switch cfg.NamingSchemaVersion {
	case namingschema.VersionV1:
		return op.buildOpNameV1(opCtx)
	default:
		return op.buildOpNameV0(opCtx)
	}
}
