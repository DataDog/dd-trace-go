// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package openfeature

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strconv"
	"sync/atomic"
	"time"

	of "github.com/open-feature/go-sdk/openfeature"

	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

// logOnboarding contains logger panics at the individual call so initialization,
// polling, and teardown continue. Recovering in the caller would skip its
// remaining work. Do not log the panic through the same failing logger.
func logOnboarding(format string, args ...any) {
	if !log.DebugEnabled() {
		return
	}
	defer recoverDebugLogging()
	log.Debug("openfeature: "+format, args...)
}

// warnRawDebugData warns, once per provider, that debug logs include raw
// evaluation data: at construction, or before the first such line when debug
// logging is enabled later. A provider shares warned with its
// debugLoggingHook; it is nil on a provider built as a bare struct literal,
// which tests do. It uses CompareAndSwap rather than sync.Once: an application
// logger may evaluate a flag, and a nested Once.Do call deadlocks.
func warnRawDebugData(warned *atomic.Bool) {
	if warned == nil || !warned.CompareAndSwap(false, true) {
		return
	}
	defer recoverDebugLogging()
	log.Warn("openfeature: debug logging is enabled, so feature flag logs include raw evaluation data (targeting keys, evaluation context attributes, flag values, and default values) that may contain personal or sensitive information. Disable the debug logging setting that enabled it (DD_TRACE_DEBUG, OTEL_LOG_LEVEL, or tracer.WithDebugMode) when you no longer need it.") //errtrack:ignore user debug configuration
}

// logResolution logs what the provider resolved for a flag under
// debug logging, with the default value and the effective evaluation context
// it resolved against. The application does not always receive the resolved
// value: debugLoggingHook logs any failure the SDK reports afterwards. It
// checks the log level before boxing detail.Value or reading flatCtx, so
// evaluations do not allocate for it while debug logging is off.
func logResolution[T any](rawDataWarned *atomic.Bool, flagKey string, flagType of.Type, defaultValue T, flatCtx of.FlattenedContext, detail of.GenericResolutionDetail[T]) {
	// The log level can change after provider construction.
	if !log.DebugEnabled() {
		return
	}
	warnRawDebugData(rawDataWarned)
	defer recoverDebugLogging()
	resolution := detail.ResolutionDetail()
	allocationKey, _ := resolution.FlagMetadata[metadataAllocationKey].(string)
	serialID := ""
	if id, ok := resolution.FlagMetadata[metadataSplitSerialIDKey].(uint32); ok {
		serialID = strconv.FormatUint(uint64(id), 10)
	}
	targetingKey, attributes := splitFlattenedContext(flatCtx)
	logOnboarding("provider resolved %q: type=%s, value=%s, default=%s, variant=%q, reason=%s, allocation=%q, serial_id=%s, error_code=%s, error_message=%q, targeting_key=%s, attributes=%s",
		flagKey,
		flagType.String(),
		formatDebugValue(detail.Value),
		formatDebugValue(defaultValue),
		resolution.Variant,
		string(resolution.Reason),
		allocationKey,
		serialID,
		string(resolution.ErrorCode),
		resolution.ErrorMessage,
		formatDebugValue(targetingKey),
		formatDebugValue(attributes),
	)
}

// splitFlattenedContext separates the targeting key from the other attributes
// of the context the provider received. targetingKey is nil when the context
// has none, so the log distinguishes a missing key from an empty one.
func splitFlattenedContext(flatCtx of.FlattenedContext) (targetingKey any, attributes map[string]any) {
	attributes = make(map[string]any, len(flatCtx))
	for k, v := range flatCtx {
		if k == of.TargetingKey {
			targetingKey = v
			continue
		}
		attributes[k] = v
	}
	return targetingKey, attributes
}

// debugLoggingHook logs every evaluation failure when debug logging is enabled, with
// the evaluation's default value and effective evaluation context. The
// provider logs its resolution itself, in logResolution: the SDK passes
// Finally hooks the provider's resolution even when an application After hook
// fails, so a Finally-based line misreports that evaluation as successful. It
// logs an application hook's error message as is.
type debugLoggingHook struct {
	of.UnimplementedHook
	rawDataWarned *atomic.Bool
}

func newDebugLoggingHook(rawDataWarned *atomic.Bool) *debugLoggingHook {
	return &debugLoggingHook{rawDataWarned: rawDataWarned}
}

// Error logs evaluation failures, including those the provider never sees,
// such as evaluating before it is ready. It names the default value without
// claiming the application received it: ObjectValueDetails keeps the resolved
// value after an After hook fails, and a multi-provider may return the
// resolved value or another provider's result.
func (h *debugLoggingHook) Error(
	_ context.Context,
	hookContext of.HookContext,
	err error,
	_ of.HookHints,
) {
	defer recoverDebugLogging()
	if !log.DebugEnabled() || err == nil {
		return
	}
	warnRawDebugData(h.rawDataWarned)
	evalCtx := hookContext.EvaluationContext()
	logOnboarding("evaluation of %q failed: %s (default value %s, targeting_key=%s, attributes=%s)",
		hookContext.FlagKey(), err.Error(), formatDebugValue(hookContext.DefaultValue()),
		formatDebugValue(evalCtx.TargetingKey()), formatDebugValue(evalCtx.Attributes()))
}

// recoverDebugLogging contains diagnostic failures in logOnboarding and in
// evaluation logging, where formatting can also panic. The OpenFeature SDK
// does not recover provider or hook panics. It logs nothing, since the logger
// may be what panicked.
func recoverDebugLogging() {
	_ = recover()
}

// formatDebugValue renders a flag value or context for a debug log line, in
// full: debug logging exposes raw evaluation data so a customer can explain a
// result. Scalars are rendered directly and anything else as JSON.
func formatDebugValue(v any) string {
	switch val := v.(type) {
	case nil:
		return "null"
	case bool:
		return strconv.FormatBool(val)
	case string:
		return strconv.Quote(val)
	case int:
		return strconv.Itoa(val)
	case int32:
		return strconv.FormatInt(int64(val), 10)
	case int64:
		return strconv.FormatInt(val, 10)
	case float32:
		return strconv.FormatFloat(float64(val), 'g', -1, 32)
	case float64:
		return strconv.FormatFloat(val, 'g', -1, 64)
	default:
		return marshalDebugValue(v)
	}
}

// marshalDebugValue renders v as JSON.
//
// The recover is necessary: v can be a caller-supplied default value whose
// MarshalJSON or MarshalText method panics, and encoding/json re-panics it.
// The OpenFeature SDK does not recover provider or hook panics either, so without
// the recover, enabling debug logging would turn a normal fallback to the
// default value into an application panic. recoverDebugLogging would also
// stop the panic, but it would drop the whole evaluation line.
func marshalDebugValue(v any) (result string) {
	defer func() {
		if recover() != nil {
			result = "<unserializable>"
		}
	}()

	data, err := json.Marshal(v)
	if err != nil {
		return "<unserializable>"
	}
	return string(data)
}

// logRejectedFlags logs each flag that configuration rejected and why, after
// the line reporting the configuration applied. source names that line's
// source, such as "agentless configuration".
func logRejectedFlags(source string, config *universalFlagsConfiguration) {
	for _, flagKey := range slices.Sorted(maps.Keys(config.invalidFlags)) {
		logOnboarding("%s rejected flag %q: %s", source, flagKey, config.invalidFlags[flagKey].Error())
	}
}

// formatCreatedAt renders a configuration's createdAt for debug logs, to help
// spot stale configuration.
func formatCreatedAt(t time.Time) string {
	if t.IsZero() {
		return "unset"
	}
	return t.UTC().Format(time.RFC3339Nano)
}
