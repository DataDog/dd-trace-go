// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package connectshared

import (
	"strings"
	"sync/atomic"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

// Config is the configuration of a connect interceptor. Its setters back the integration's
// options; Init completes it once they have run, and it is read-only afterwards.
type Config struct {
	instr     *instrumentation.Instrumentation
	pkg       instrumentation.Package
	logPrefix string
	lib       *Library

	// service is set by SetService, or by resolveService once the tracer is initialized.
	service atomic.Pointer[resolvedService]
	// defaultService is the default service that resolveService last resolved before then.
	defaultService      atomic.Pointer[resolvedService]
	nonErrorCodes       map[Code]bool
	errCheck            func(procedure string, err error) bool
	traceStreamCalls    bool
	traceStreamMessages bool
	noDebugStack        bool
	untracedMethods     map[string]struct{}
	withMetadataTags    bool
	ignoredMetadata     map[string]struct{}
	withRequestTags     bool
	withErrorDetailTags bool
	analyticsRate       float64
	spanOpts            []tracer.StartSpanOption
	tags                map[string]any

	// Built by Init once every setter has run; read-only afterwards.
	client, server spanBases
	customTags     tracer.StartSpanOption

	// starts caches the start options of startTags.
	starts startCache
}

// NewConfig returns the default configuration of an interceptor of the integration pkg, loaded as
// instr. Its log messages start with the integration's path, "contrib/" followed by pkg, and lib,
// which must not be nil, describes the errors of the connect major version that pkg traces.
func NewConfig(instr *instrumentation.Instrumentation, pkg instrumentation.Package, lib *Library) *Config {
	return &Config{
		instr:               instr,
		pkg:                 pkg,
		logPrefix:           "contrib/" + string(pkg),
		lib:                 lib,
		nonErrorCodes:       map[Code]bool{CodeCanceled: true},
		traceStreamCalls:    true,
		traceStreamMessages: true,
		analyticsRate:       instr.AnalyticsRate(false),
		ignoredMetadata: map[string]struct{}{
			"authorization":               {},
			"baggage":                     {},
			"b3":                          {},
			"cookie":                      {},
			"proxy-authorization":         {},
			"set-cookie":                  {},
			"traceparent":                 {},
			"tracestate":                  {},
			"x-api-key":                   {},
			"x-auth-token":                {},
			"x-b3-flags":                  {},
			"x-b3-parentspanid":           {},
			"x-b3-sampled":                {},
			"x-b3-spanid":                 {},
			"x-b3-traceid":                {},
			"x-datadog-origin":            {},
			"x-datadog-parent-id":         {},
			"x-datadog-sampling-priority": {},
			"x-datadog-tags":              {},
			"x-datadog-trace-id":          {},
		},
	}
}

// Init builds the span configuration from the settings.
func (c *Config) Init() {
	c.client = newSpanBases(instrumentation.ComponentClient, c.pkg, c.analyticsRate)
	c.server = newSpanBases(instrumentation.ComponentServer, c.pkg, c.analyticsRate)
	if len(c.tags) > 0 {
		c.customTags = tracer.WithTags(c.tags)
	}
}

// SetService sets the service of every span.
func (c *Config) SetService(name string) {
	c.service.Store(newResolvedService(name, instrumentation.ServiceSourceWithServiceOption))
}

// SetStreamCalls enables or disables call spans for streaming RPCs.
func (c *Config) SetStreamCalls(enabled bool) {
	c.traceStreamCalls = enabled
}

// SetStreamMessages enables or disables a span per streamed message.
func (c *Config) SetStreamMessages(enabled bool) {
	c.traceStreamMessages = enabled
}

// SetNoDebugStack disables stack traces for errors.
func (c *Config) SetNoDebugStack() {
	c.noDebugStack = true
}

// SetNonErrorCodes replaces the set of codes that are not recorded as errors. C is a connect Code.
func SetNonErrorCodes[C ~uint32](c *Config, codes []C) {
	c.nonErrorCodes = make(map[Code]bool, len(codes))
	for _, code := range codes {
		c.nonErrorCodes[Code(code)] = true
	}
}

// SetErrorCheck sets fn to decide whether an error that the non-error codes did not suppress is
// recorded.
func (c *Config) SetErrorCheck(fn func(procedure string, err error) bool) {
	c.errCheck = fn
}

// MethodSet returns the set of methods, for SetUntracedMethods.
func MethodSet(methods ...string) map[string]struct{} {
	set := make(map[string]struct{}, len(methods))
	for _, method := range methods {
		set[method] = struct{}{}
	}
	return set
}

// SetUntracedMethods replaces the set of procedures for which no spans are created. methods is not
// copied.
func (c *Config) SetUntracedMethods(methods map[string]struct{}) {
	c.untracedMethods = methods
}

// EnableMetadataTags enables SetMetadataTags.
func (c *Config) EnableMetadataTags() {
	c.withMetadataTags = true
}

// IgnoreMetadata adds case-insensitive metadata keys to exclude from SetMetadataTags.
func (c *Config) IgnoreMetadata(keys ...string) {
	for _, key := range keys {
		c.ignoredMetadata[strings.ToLower(key)] = struct{}{}
	}
}

// EnableRequestTags enables SetRequestTags.
func (c *Config) EnableRequestTags() {
	c.withRequestTags = true
}

// EnableErrorDetailTags enables tagging spans with the protobuf details of recorded errors.
func (c *Config) EnableErrorDetailTags() {
	c.withErrorDetailTags = true
}

// AddCustomTag adds a tag to every span, replacing an earlier value of key.
func (c *Config) AddCustomTag(key string, value any) {
	if c.tags == nil {
		c.tags = make(map[string]any)
	}
	c.tags[key] = value
}

// AddSpanOptions adds options to every span.
func (c *Config) AddSpanOptions(opts ...tracer.StartSpanOption) {
	c.spanOpts = append(c.spanOpts, opts...)
}

// StreamCalls reports whether streaming RPCs get a call span.
func (c *Config) StreamCalls() bool {
	return c.traceStreamCalls
}

// StreamMessages reports whether streamed messages get a span.
func (c *Config) StreamMessages() bool {
	return c.traceStreamMessages
}

// MetadataTags reports whether SetMetadataTags is enabled.
func (c *Config) MetadataTags() bool {
	return c.withMetadataTags
}

// RequestTags reports whether SetRequestTags is enabled.
func (c *Config) RequestTags() bool {
	return c.withRequestTags
}

// resolvedService is an immutable service name paired with its prebuilt start tag.
type resolvedService struct {
	name string
	// override is the value of the ext.KeyServiceSource start tag, which opt sets.
	override any
	opt      tracer.StartSpanOption
}

// newResolvedService builds the same option as instrumentation.ServiceNameWithSource, without
// allocating on every span.
func newResolvedService(name, source string) *resolvedService {
	var override any = instrumentation.ServiceOverride{Name: name, Source: source}
	return &resolvedService{name: name, override: override, opt: tracer.Tag(ext.KeyServiceSource, override)}
}

// resolveService returns the SetService value or the default (DD_SERVICE). The default is only
// cached once the tracer is initialized: before tracer.Start, which Orchestrion runs after
// package-level clients and servers are built, it is still "". Until then, the last default is
// reused while its name is unchanged, so that spans share it, and the start options cached for it.
func (c *Config) resolveService() *resolvedService {
	if svc := c.service.Load(); svc != nil {
		return svc
	}
	name := c.instr.ServiceName(instrumentation.ComponentDefault, nil)
	svc := c.defaultService.Load()
	if svc == nil || svc.name != name {
		source := string(c.pkg)
		if name == "" {
			// The tracer's own default service is not an override.
			source = ""
		}
		svc = newResolvedService(name, source)
		c.defaultService.Store(svc)
	}
	if c.instr.TracerInitialized() {
		c.service.Store(svc)
	}
	return svc
}
