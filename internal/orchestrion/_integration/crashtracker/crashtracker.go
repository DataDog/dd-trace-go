// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker

import (
	"context"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCase verifies that crashtracker.Start() is injected into main() by
// orchestrion. The crashtracker does not produce trace spans, so the test
// only validates that the instrumented binary runs without crashing due to
// a missing Start() call.
type TestCase struct{}

func (*TestCase) Setup(_ context.Context, _ *testing.T) {}

func (*TestCase) Run(_ context.Context, _ *testing.T) {}

func (*TestCase) ExpectedTraces() trace.Traces {
	return trace.Traces{}
}
