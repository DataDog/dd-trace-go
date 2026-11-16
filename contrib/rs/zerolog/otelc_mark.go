// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package zerolog

import (
	"context"

	"github.com/rs/zerolog"

	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

// otelcEnabled is a variable so tests can turn the otelc behavior on.
var otelcEnabled = instrumentation.OtelcEnabled

type correlatedKey struct{}

// correlatedByOther, in an otelc build, reports whether another
// DDContextLogHook already added the trace fields to e.
//
// The otelc rules add a DDContextLogHook to every logger built with
// zerolog.New, so a logger the application also sets up with this hook runs it
// twice for each event. Plain builds skip all of it.
func correlatedByOther(ctx context.Context, e *zerolog.Event) bool {
	return otelcEnabled() && ctx.Value(correlatedKey{}) == e
}

// markCorrelated, in an otelc build, marks the context of e so that the next
// DDContextLogHook of the same event adds nothing. The mark holds e, so another
// event that reuses this context, for example from a hook that logs it to
// another logger, is still correlated.
func markCorrelated(ctx context.Context, e *zerolog.Event) {
	if !otelcEnabled() {
		return
	}
	e.Ctx(context.WithValue(ctx, correlatedKey{}, e))
}
