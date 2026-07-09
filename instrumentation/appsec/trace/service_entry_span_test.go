// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package trace

import (
	"context"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion"
)

func TestServiceEntrySpanOperationFinishClearsGLS(t *testing.T) {
	t.Cleanup(orchestrion.MockGLS())

	const iterations = 1000
	baseline := orchestrion.GLSStackDepth()

	for range iterations {
		op, _ := StartServiceEntrySpanOperation(context.Background(), NoopTagSetter{})
		op.Finish()

		if depth := orchestrion.GLSStackDepth(); depth != baseline {
			t.Fatalf("GLS depth after ServiceEntrySpanOperation.Finish() = %d, want baseline %d", depth, baseline)
		}
	}
}
