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

func TestSetSerializableTagNativeInt(t *testing.T) {
	ts := TestTagSetter{}
	op, _ := StartServiceEntrySpanOperation(context.Background(), ts)

	op.SetSerializableTag("int_tag", 200)
	op.SetSerializableTag("uint_tag", uint(7))

	if _, ok := op.jsonTags["int_tag"]; ok {
		t.Fatalf("native int tag was JSON-serialized (jsonTags), want direct tag")
	}
	if _, ok := op.jsonTags["uint_tag"]; ok {
		t.Fatalf("native uint tag was JSON-serialized (jsonTags), want direct tag")
	}
	if ts["int_tag"] != 200 {
		t.Fatalf("int_tag not set directly: got %v", ts["int_tag"])
	}
	if ts["uint_tag"] != uint(7) {
		t.Fatalf("uint_tag not set directly: got %v", ts["uint_tag"])
	}
}
