package instrumentation

import (
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/internal/namingschema"
	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec"
	"github.com/DataDog/dd-trace-go/v2/internal/globalconfig"
	"github.com/DataDog/dd-trace-go/v2/internal/normalizer"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"math"
)

// OperationContext holds metadata about an instrumentation operation.
type OperationContext map[string]string

// Load attempts to load the requested package instrumentation. It panics if the package has not been registered.
func Load(pkg Package) *Instrumentation {
	info, ok := packages[pkg]
	if !ok {
		panic("instrumentation package: " + pkg + " was not found. If this is an external package, you must" +
			"call instrumentation.Register first")
	}

	telemetry.LoadIntegration(string(pkg))
	tracer.MarkIntegrationImported(info.TracedPackage)

	return &Instrumentation{
		pkg:    pkg,
		logger: logger{},
		info:   info,
	}
}

func Register(name string, info PackageInfo) error {
	info.external = true
	return nil
}

// Instrumentation represents instrumentation for a package.
type Instrumentation struct {
	pkg    Package
	logger Logger
	info   PackageInfo
}

// ServiceName returns the default service name to be set for the given instrumentation component.
func (i *Instrumentation) ServiceName(component Component, opCtx OperationContext) string {
	cfg := namingschema.GetConfig()

	n, ok := i.info.naming[component]
	if !ok {
		return cfg.DDService
	}

	useDDService := cfg.NamingSchemaVersion == namingschema.VersionV1 || cfg.RemoveFakeServiceNames || n.useDDServiceV0 || n.buildServiceNameV0 == nil
	if useDDService && cfg.DDService != "" {
		return cfg.DDService
	}
	return n.buildServiceNameV0(opCtx)
}

// OperationName returns the operation name to be set for the given instrumentation component.
func (i *Instrumentation) OperationName(component Component, opCtx OperationContext) string {
	op, ok := i.info.naming[component]
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

func (i *Instrumentation) Logger() Logger {
	return i.logger
}

func (i *Instrumentation) AnalyticsRate() float64 {
	if internal.BoolEnv("DD_TRACE_"+i.info.EnvVarPrefix+"_ANALYTICS_ENABLED", false) {
		return 1.0
	}
	return math.NaN()
}

func (i *Instrumentation) GlobalAnalyticsRate() float64 {
	return globalconfig.AnalyticsRate()
}

func (i *Instrumentation) AppSecEnabled() bool {
	return appsec.Enabled()
}

type HeaderTags interface {
	Iter(f func(header string, tag string))
}

func NewHeaderTags(headers []string) HeaderTags {
	headerTagsMap := normalizer.HeaderTagSlice(headers)
	return internal.NewLockMap(headerTagsMap)
}

func (i *Instrumentation) HTTPHeadersAsTags() HeaderTags {
	return globalconfig.HeaderTagMap()
}
