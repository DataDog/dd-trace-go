// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package openfeature

import (
	"context"
	"encoding/json"
	"strconv"

	of "github.com/open-feature/go-sdk/openfeature"

	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

// maxDebugValueLen bounds how much of a flag value one debug line carries, so
// a large JSON flag cannot flood the customer's log.
const maxDebugValueLen = 256

// debugLoggingHook logs the outcome of every flag evaluation under
// DD_TRACE_DEBUG, so a customer can see why an evaluation returned what it
// did. It uses the Finally stage, like flagEvalMetricsHook, so errored and
// defaulted evaluations are logged too. The targeting key and context
// attributes are never logged.
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
	// The level can be lowered after the hook was registered at construction.
	if !log.DebugEnabled() {
		return
	}
	allocationKey, _ := details.FlagMetadata[metadataAllocationKey].(string)
	log.Debug("openfeature: evaluated %q: type=%s, value=%s, variant=%q, reason=%s, allocation=%q, error_code=%s, error_message=%q",
		hookContext.FlagKey(),
		details.FlagType.String(),
		formatDebugValue(details.Value),
		details.Variant,
		string(details.Reason),
		allocationKey,
		string(details.ErrorCode),
		details.ErrorMessage,
	)
}

// formatDebugValue renders a flag value for a debug log line without the
// reflection-based %v verb, truncated to maxDebugValueLen bytes.
func formatDebugValue(v any) string {
	var s string
	switch val := v.(type) {
	case nil:
		s = "null"
	case bool:
		s = strconv.FormatBool(val)
	case string:
		s = strconv.Quote(val)
	case int64:
		s = strconv.FormatInt(val, 10)
	case int:
		s = strconv.Itoa(val)
	case float64:
		s = strconv.FormatFloat(val, 'g', -1, 64)
	default:
		b, err := json.Marshal(val)
		if err != nil {
			return "<unserializable>"
		}
		s = string(b)
	}
	if len(s) > maxDebugValueLen {
		return s[:maxDebugValueLen] + "...(truncated)"
	}
	return s
}
