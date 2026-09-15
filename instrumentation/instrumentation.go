// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package instrumentation

import (
	"context"
	"math"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec"
	"github.com/DataDog/dd-trace-go/v2/internal/globalconfig"
	"github.com/DataDog/dd-trace-go/v2/internal/namingschema"
	"github.com/DataDog/dd-trace-go/v2/internal/normalizer"
	"github.com/DataDog/dd-trace-go/v2/internal/stableconfig"
	"github.com/DataDog/dd-trace-go/v2/internal/stacktrace"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	telemetrylog "github.com/DataDog/dd-trace-go/v2/internal/telemetry/log"
	"github.com/DataDog/dd-trace-go/v2/internal/version"
)

// OperationContext holds metadata about an instrumentation operation.
type OperationContext map[string]string

// Register registers a new [Package] instrumentation. Panics if called multiple
// times for the same [Package].
func Register(pkg Package, info PackageInfo) {
	packagesMu.Lock()
	defer packagesMu.Unlock()

	if _, ok := packages[pkg]; ok {
		panic("instrumentation package: " + pkg + " was already registered.")
	}
	info.external = true // Marker for external packages
	packages[pkg] = info
}

// RegisterAndLoad registers a new [Package] instrumentation and immediately
// loads it, returning the associated [Instrumentation] instance. Panics if the
// package has already been registered previously.
func RegisterAndLoad(pkg Package, info PackageInfo) *Instrumentation {
	Register(pkg, info)
	return Load(pkg)
}

// Load attempts to load the requested package instrumentation. It panics if the package has not been registered.
func Load(pkg Package) *Instrumentation {
	packagesMu.RLock()
	defer packagesMu.RUnlock()

	info, ok := packages[pkg]
	if !ok {
		panic("instrumentation package: " + pkg + " was not found. If this is an external package, you must " +
			"call instrumentation.Register first")
	}

	telemetry.LoadIntegration(string(pkg))
	tracer.MarkIntegrationImported(info.TracedPackage)

	return &Instrumentation{
		logger:           newLogger(pkg),
		telemetrylog:     telemetrylog.With(telemetry.WithTags([]string{"integration:" + string(pkg)})),
		telemetryMetrics: telemetryMetrics{valid: true},

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
	logger           Logger
	telemetrylog     *telemetrylog.Logger
	telemetryMetrics telemetryMetrics

	pkg  Package
	info PackageInfo
}

// ServiceName returns the default service name to be set for the given instrumentation component.
// When the package has no naming entry, which is the recommended setup for new integrations, this
// returns the global DD_SERVICE. The per-component naming logic backs the legacy
// DD_TRACE_SPAN_ATTRIBUTE_SCHEMA feature.
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

const (
	// ServiceSourceWithServiceOption is the service source value used when the service
	// name is explicitly set via a WithService option.
	ServiceSourceWithServiceOption = "opt.with_service"
)

// ServiceOverride bundles a service name with its source for use with
// span.SetTag(ext.KeyServiceSource, instrumentation.ServiceOverride{...}).
// This should be used instead of span.SetTag(ext.ServiceName, ...) to preserve
// the service source information.
type ServiceOverride = internal.ServiceOverride

// ServiceNameWithSource returns a StartSpanOption that sets both the service
// name and its source. The source tracks the origin of the service name
// override for _dd.svc_src.
func ServiceNameWithSource(name string, source string) tracer.StartSpanOption {
	return func(cfg *tracer.StartSpanConfig) {
		tracer.Tag(ext.KeyServiceSource, internal.ServiceOverride{Name: name, Source: source})(cfg)
	}
}

// OperationName returns the operation name to be set for the given instrumentation component. It
// backs the legacy DD_TRACE_SPAN_ATTRIBUTE_SCHEMA naming-schema feature; new integrations should not
// call it and should hardcode operation names as string literals instead. See contrib/INTEGRATIONS.md.
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

// TelemetryMetrics returns the [TelemetryMetricsClient] that instrumentation
// should use to submit internal telemetry metrics data.
//
// IMPORTANT: If you are not sure what this is for, you should probably be using
// [*Instrumentation.StatsdClient] instead.
func (i *Instrumentation) TelemetryMetrics() TelemetryMetricsClient {
	return &i.telemetryMetrics
}

type TelemetryNamespace = telemetry.Namespace

const (
	// TelemetryNamespaceIAST is the namespace for IAST telemetry
	TelemetryNamespaceIAST = telemetry.NamespaceIAST
)

// TelemetryProductStarted declares a telemetry product as having started.
func (i *Instrumentation) TelemetryProductStarted(ns TelemetryNamespace) {
	telemetry.ProductStarted(ns)
}

// TelemetryProductStartError declares a telemetry product as having failed to
// start because of the specified error.
func (i *Instrumentation) TelemetryProductStartError(ns TelemetryNamespace, err error) {
	telemetry.ProductStartError(ns, err)
}

// TelemetryProductStopped declares a telemetry product as having stopped.
func (i *Instrumentation) TelemetryProductStopped(ns TelemetryNamespace) {
	telemetry.ProductStopped(ns)
}

type TelemetryOrigin = telemetry.Origin

const (
	TelemetryOriginDefault = telemetry.OriginDefault
	TelemetryOriginEnvVar  = telemetry.OriginEnvVar
)

func (i *Instrumentation) TelemetryRegisterAppConfig(key string, value any, origin TelemetryOrigin) {
	telemetry.RegisterAppConfig(key, value, origin)
}

type AppEndpointAttributes = telemetry.AppEndpointAttributes

func (i *Instrumentation) TelemetryRegisterAppEndpoint(opName string, resName string, attrs AppEndpointAttributes) {
	telemetry.RegisterAppEndpoint(opName, resName, attrs)
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

func (i *Instrumentation) APISecurityEndpointCollectionEnabled() bool {
	return internal.BoolEnv("DD_API_SECURITY_ENDPOINT_COLLECTION_ENABLED", true)
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

// PopExecutionTraced pops the top executionTracedKey from the GLS stack.
// Must be paired with WithExecutionTraced when the traced scope ends.
func (i *Instrumentation) PopExecutionTraced() {
	internal.PopExecutionTraced()
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

// StackTrace is a stack-trace event captured by an [Instrumentation]. Its
// fields must not be mutated after passing it to [Instrumentation.RecordStackTrace].
type StackTrace = stacktrace.Event

// StackFrame is a single frame of a [StackTrace].
type StackFrame = stacktrace.StackFrame

// StackTraceCategory identifies the kind of event associated with a stack trace.
type StackTraceCategory = stacktrace.EventCategory

const (
	// StackTraceCategoryException identifies an exception stack trace.
	StackTraceCategoryException StackTraceCategory = stacktrace.ExceptionEvent
	// StackTraceCategoryVulnerability identifies a vulnerability stack trace.
	StackTraceCategoryVulnerability StackTraceCategory = stacktrace.VulnerabilityEvent
	// StackTraceCategoryExploit identifies an exploit stack trace.
	StackTraceCategoryExploit StackTraceCategory = stacktrace.ExploitEvent
)

// StackTraceOption configures a captured stack-trace event. Values are created
// by the WithStackTrace functions in this package.
type StackTraceOption = stacktrace.Option

// WithStackTraceType sets the event type.
func WithStackTraceType(eventType string) StackTraceOption {
	return stacktrace.WithType(eventType)
}

// WithStackTraceMessage sets the event message.
func WithStackTraceMessage(message string) StackTraceOption {
	return stacktrace.WithMessage(message)
}

// WithStackTraceID sets the event correlation ID. Vulnerability and exploit
// stack traces require a non-empty ID.
func WithStackTraceID(id string) StackTraceOption {
	return stacktrace.WithID(id)
}

// WithStackTraceSkip sets the number of caller frames to skip after the
// stacktrace capture machinery. Negative values are treated as zero.
func WithStackTraceSkip(skip int) StackTraceOption {
	return stacktrace.WithSkip(skip)
}

// WithStackTraceDepth sets the maximum number of frames to capture. A
// non-positive depth uses the default depth.
func WithStackTraceDepth(depth int) StackTraceOption {
	return stacktrace.WithDepth(depth)
}

// CaptureStackTrace captures a stack trace for an event in category. It returns
// nil when no frames are captured or when a vulnerability or exploit event has
// no correlation ID. The caller is responsible for applying its product-specific
// enablement configuration.
func (i *Instrumentation) CaptureStackTrace(category StackTraceCategory, options ...StackTraceOption) *StackTrace {
	// We need to skip the frame for the call to [*Instrumentation.CaptureStackTrace] itself.
	options = append(options, stacktrace.WithAdditionalSkip(1))
	event := stacktrace.NewEvent(category, options...)
	if !validStackTrace(event) {
		return nil
	}
	return event
}

// RecordStackTrace submits trace to the local root span's _dd.stack meta_struct
// entry. Calls from IAST, AppSec, and other producers are aggregated by event
// category and encoded when the span is serialized. Nil traces, traces without
// frames, and vulnerability or exploit traces without an ID are ignored. The
// caller is responsible for product-specific enablement.
//
// The return value reports whether the trace passed validation and was submitted
// to the root span. The span API does not report whether a finished root rejected
// the tag.
func (i *Instrumentation) RecordStackTrace(span *tracer.Span, trace *StackTrace) bool {
	if span == nil || !validStackTrace(trace) {
		return false
	}

	root := span.Root()
	if root == nil {
		return false
	}
	return stacktrace.AddToSpan(root, trace)
}

func validStackTrace(trace *StackTrace) bool {
	if trace == nil || len(trace.Frames) == 0 {
		return false
	}
	if trace.Category == StackTraceCategoryVulnerability || trace.Category == StackTraceCategoryExploit {
		return trace.ID != ""
	}
	return true
}
