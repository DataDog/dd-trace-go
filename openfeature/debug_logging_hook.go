// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package openfeature

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	of "github.com/open-feature/go-sdk/openfeature"

	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

// maxDebugValueLen bounds how much of a flag value one debug line carries, so
// a large string or JSON flag cannot flood the customer's log.
const maxDebugValueLen = 256

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

// debugLoggingHook logs the outcome of every flag evaluation under
// DD_TRACE_DEBUG, so a customer can see why an evaluation returned what it
// did. It uses the Finally stage, like flagEvalMetricsHook, so errored and
// defaulted evaluations are logged too, and the Error stage for failures the
// SDK reports only there. It never logs the targeting key or context
// attributes itself, but logs an application hook's error message as is.
type debugLoggingHook struct {
	of.UnimplementedHook
}

func newDebugLoggingHook() *debugLoggingHook {
	return &debugLoggingHook{}
}

// Finally logs the flag evaluation details.
func (h *debugLoggingHook) Finally(
	_ context.Context,
	hookContext of.HookContext,
	details of.InterfaceEvaluationDetails,
	_ of.HookHints,
) {
	defer recoverDebugLogging()
	// The log level can change after provider construction.
	if !log.DebugEnabled() {
		return
	}
	allocationKey, _ := details.FlagMetadata[metadataAllocationKey].(string)
	serialID := ""
	if id, ok := details.FlagMetadata[metadataSplitSerialIDKey].(uint32); ok {
		serialID = strconv.FormatUint(uint64(id), 10)
	}
	logOnboarding("evaluated %q: type=%s, value=%s, variant=%q, reason=%s, allocation=%q, serial_id=%s, error_code=%s, error_message=%q",
		hookContext.FlagKey(),
		details.FlagType.String(),
		formatDebugValue(details.Value),
		details.Variant,
		string(details.Reason),
		allocationKey,
		serialID,
		string(details.ErrorCode),
		details.ErrorMessage,
	)
}

// Error logs evaluation failures. The SDK reports some, such as evaluating
// before the provider is ready, only to Error hooks: Finally then receives
// details with no reason or error code.
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
	logOnboarding("evaluation of %q failed: %s", hookContext.FlagKey(), err.Error())
}

// recoverDebugLogging contains diagnostic failures in logOnboarding and in
// evaluation hooks, where formatting can also panic. The OpenFeature SDK does
// not recover hook panics. It logs nothing, since the logger may be what panicked.
func recoverDebugLogging() {
	_ = recover()
}

// formatDebugValue renders a flag value for a debug log line, truncated to
// maxDebugValueLen bytes. Scalars are rendered directly and anything else as
// JSON.
func formatDebugValue(v any) string {
	var s string
	switch val := v.(type) {
	case nil:
		s = "null"
	case bool:
		s = strconv.FormatBool(val)
	case string:
		if len(val) > maxDebugValueLen {
			val = val[:maxDebugValueLen]
		}
		s = strconv.Quote(val)
	case int:
		s = strconv.Itoa(val)
	case int32:
		s = strconv.FormatInt(int64(val), 10)
	case int64:
		s = strconv.FormatInt(val, 10)
	case float32:
		s = strconv.FormatFloat(float64(val), 'g', -1, 32)
	case float64:
		s = strconv.FormatFloat(val, 'g', -1, 64)
	default:
		return marshalDebugValue(v)
	}
	if len(s) > maxDebugValueLen {
		return s[:maxDebugValueLen] + "...(truncated)"
	}
	return s
}

// marshalDebugValue renders v as JSON, truncated to maxDebugValueLen bytes.
//
// The recover is necessary: v can be a caller-supplied default value whose
// MarshalJSON or MarshalText method panics, and encoding/json re-panics it.
// The OpenFeature SDK does not recover panics from hooks either, so without
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
	if len(data) > maxDebugValueLen {
		return string(data[:maxDebugValueLen]) + "...(truncated)"
	}
	return string(data)
}

// formatCreatedAt renders a configuration's createdAt for debug logs, to help
// spot stale configuration.
func formatCreatedAt(t time.Time) string {
	if t.IsZero() {
		return "unset"
	}
	return t.UTC().Format(time.RFC3339Nano)
}
