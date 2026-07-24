// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package tracer

// newTracer is the package-test activation helper. It follows the production
// Store commit, global-tracer swap, compatibility bridge, activation, telemetry,
// and retirement path; tests that only need construction must use
// newUnpublishedTracer or newTestConfig.
func newTracer(opts ...StartOption) (*tracer, error) {
	t, err := newUnpublishedTracer(opts...)
	if err != nil {
		return nil, err
	}
	if err := t.publishAndActivate(t, false); err != nil {
		return nil, err
	}
	return t, nil
}
