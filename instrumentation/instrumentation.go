// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package instrumentation

import (
	"context"
	"math"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec"
	"github.com/DataDog/dd-trace-go/v2/internal/globalconfig"
	"github.com/DataDog/dd-trace-go/v2/internal/namingschema"
	"github.com/DataDog/dd-trace-go/v2/internal/normalizer"
	"github.com/DataDog/dd-trace-go/v2/internal/stableconfig"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	telemetrylog "github.com/DataDog/dd-trace-go/v2/internal/telemetry/log"
	"github.com/DataDog/dd-trace-go/v2/internal/version"
)

// OperationContext holds metadata about an instrumentation operation.
type OperationContext map[string]string

// Load attempts to load the requested package instrumentation. It panics if the package has not been registered.
func Load(pkg Package) *Instrumentation {
	info, ok := packages[pkg]
	if !ok {
		panic("instrumentation package: " + pkg + " was not found. If this is an external package, you must " +
			"call instrumentation.Register first")
	}

	telemetry.LoadIntegration(string(pkg))
	tracer.MarkIntegrationImported(info.TracedPackage)

	return &Instrumentation{
		logger:       newLogger(pkg),
		telemetrylog: telemetrylog.With(telemetry.WithTags([]string{"integration:" + string(pkg)})),

		pkg:  pkg,
		info: info,
	}
}

// ReloadConfig reloads config read from environment variables. This is useful for tests.
func ReloadConfig() {
	namingschema.ReloadConfig()
}

// Version returns the version of the dd-trace-go package.
func Version() string {
	return version.Tag
}

// Instrumentation represents instrumentation for a package.
type Instrumentation struct {
	logger       Logger
	telemetrylog *telemetrylog.Logger

	pkg  Package
	info PackageInfo
}

// ServiceName returns the default service name to be set for the given instrumentation component.
func (i *Instrumentation) ServiceName(component Component, opCtx OperationContext) string {
	cfg := namingschema.GetConfig()

	n, ok := i.info.naming[component]
	if !ok {
		return cfg.DDService
	}

	useDDService := cfg.NamingSchemaVersion == namingschema.SchemaV1 || cfg.RemoveIntegrationServiceNames || n.useDDServiceV0 || n.buildServiceNameV0 == nil
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

	switch namingschema.GetVersion() {
	case namingschema.SchemaV1:
		return op.buildOpNameV1(opCtx)
	default:
		return op.buildOpNameV0(opCtx)
	}
}

func (i *Instrumentation) Logger() Logger {
	return i.logger
}

func (i *Instrumentation) TelemetryLog() *telemetrylog.Logger {
	return i.telemetrylog
}

type TelemetryOrigin = telemetry.Origin

const (
	TelemetryOriginDefault = telemetry.OriginDefault
	TelemetryOriginEnvVar  = telemetry.OriginEnvVar
)

func (i *Instrumentation) TelemetryRegisterAppConfig(key string, value any, origin TelemetryOrigin) {
	telemetry.RegisterAppConfig(key, value, origin)
}

func (i *Instrumentation) AnalyticsRate(defaultGlobal bool) float64 {
	if internal.BoolEnv("DD_TRACE_"+i.info.EnvVarPrefix+"_ANALYTICS_ENABLED", false) {
		return 1.0
	}
	if defaultGlobal {
		return i.GlobalAnalyticsRate()
	}
	return math.NaN()
}

func (i *Instrumentation) GlobalAnalyticsRate() float64 {
	return globalconfig.AnalyticsRate()
}

func (i *Instrumentation) AppSecEnabled() bool {
	return appsec.Enabled()
}

func (i *Instrumentation) AppSecRASPEnabled() bool {
	return appsec.RASPEnabled()
}

func (i *Instrumentation) DataStreamsEnabled() bool {
	v, _, _ := stableconfig.Bool("DD_DATA_STREAMS_ENABLED", false)
	return v
}

// TracerInitialized returns whether the global tracer has been initialized or not.
func (i *Instrumentation) TracerInitialized() bool {
	return internal.TracerInitialized()
}

// WithExecutionTraced marks ctx as being associated with an execution trace
// task. It is assumed that ctx already contains a trace task. The caller is
// responsible for ending the task.
//
// This is intended for a specific case where the database/sql contrib package
// only creates spans *after* an operation, in case the operation was
// unavailable, and thus execution trace tasks tied to the span only capture the
// very end. This function enables creating a task *before* creating a span, and
// communicating to the APM tracer that it does not need to create a task. In
// general, APM instrumentation should prefer creating tasks around the
// operation rather than after the fact, if possible.
func (i *Instrumentation) WithExecutionTraced(ctx context.Context) context.Context {
	return internal.WithExecutionTraced(ctx)
}

type StatsdClient = internal.StatsdClient

func (i *Instrumentation) StatsdClient(extraTags []string) (StatsdClient, error) {
	addr := globalconfig.DogstatsdAddr()
	tags := globalconfig.StatsTags()
	tags = append(tags, extraTags...)
	return internal.NewStatsdClient(addr, tags)
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

func (i *Instrumentation) ActiveSpanKey() any {
	return internal.ActiveSpanKey
}
