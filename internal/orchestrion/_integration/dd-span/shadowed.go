// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package ddspan

// This file imports neither the tracer nor context, so the instrumentation adds
// them. The functions below declare names that match those packages, which
// the generated code must not resolve to.

//dd:span
func spanWithShadowingParams(tracer, context string) string {
	return tracer + context
}

type shadowingReceiver struct{}

//dd:span
func (context *shadowingReceiver) spanWithShadowingReceiver() (tracer error) {
	_ = context
	return nil
}
