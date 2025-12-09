// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package trace

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/xlab/treeprint"
)

func TestTrace_matchesAny(t *testing.T) {
	// 2 producer root spans with 1 consumer child span respectively
	want := Traces{
		{
			Meta: map[string]string{
				"component": "segmentio/kafka.go.v0",
				"span.kind": "producer",
			},
			Tags: map[string]any{
				"name":     "kafka.produce",
				"resource": "Produce Topic topic-A",
				"service":  "kafka",
				"type":     "queue",
			},
			Children: Traces{
				{
					Meta: map[string]string{
						"component": "segmentio/kafka.go.v0",
						"span.kind": "consumer",
					},
					Tags: map[string]any{
						"name":     "kafka.consume",
						"resource": "Consume Topic topic-A",
						"service":  "kafka",
						"type":     "queue",
					},
				},
			},
		},
		{
			Meta: map[string]string{
				"component": "segmentio/kafka.go.v0",
				"span.kind": "producer",
			},
			Tags: map[string]any{
				"name":     "kafka.produce",
				"resource": "Produce Topic topic-B",
				"service":  "kafka",
				"type":     "queue",
			},
			Children: Traces{
				{
					Meta: map[string]string{
						"component": "segmentio/kafka.go.v0",
						"span.kind": "consumer",
					},
					Tags: map[string]any{
						"name":     "kafka.consume",
						"resource": "Consume Topic topic-B",
						"service":  "kafka",
						"type":     "queue",
					},
				},
			},
		},
	}
	// got the same 4 spans, but the parent-child relationships are different
	got := Traces{
		{
			SpanID: 6461804313269386728,
			Meta: map[string]string{
				"messaging.system": "kafka",
				"span.kind":        "producer",
				"component":        "segmentio/kafka.go.v0",
			},
			Tags: map[string]any{
				"name":     "kafka.produce",
				"resource": "Produce Topic topic-A",
				"service":  "kafka",
				"type":     "queue",
			},
			Children: Traces{
				{
					SpanID: 1242110709011053063,
					Meta: map[string]string{
						"messaging.system": "kafka",
						"span.kind":        "producer",
						"component":        "segmentio/kafka.go.v0",
					},
					Tags: map[string]any{
						"name":     "kafka.produce",
						"resource": "Produce Topic topic-B",
						"service":  "kafka",
						"type":     "queue",
					},
					Children: Traces{
						{
							SpanID: 7873578434319770271,
							Meta: map[string]string{
								"messaging.system": "kafka",
								"span.kind":        "consumer",
								"component":        "segmentio/kafka.go.v0",
							},
							Tags: map[string]any{
								"name":     "kafka.consume",
								"resource": "Consume Topic topic-B",
								"service":  "kafka",
								"type":     "queue",
							},
							Children: nil,
						},
					},
				},
				{
					SpanID: 6458862152963979372,
					Meta: map[string]string{
						"messaging.system": "kafka",
						"span.kind":        "consumer",
						"component":        "segmentio/kafka.go.v0",
					},
					Tags: map[string]any{
						"name":     "kafka.consume",
						"resource": "Consume Topic topic-A",
						"service":  "kafka",
						"type":     "queue",
					},
					Children: nil,
				},
			},
		},
	}

	{
		// the first one should be ok, since there is a root span produce-A with a child consume-A
		w := want[0]
		foundTrace, diff := w.matchesAny(got, treeprint.NewWithRoot("Root"))
		assert.NotNil(t, foundTrace, "trace was not found")
		assert.Empty(t, diff)
	}
	{
		// the second should not be ok, as it's not a root span.
		w := want[1]
		foundTrace, diff := w.matchesAny(got, treeprint.NewWithRoot("Root"))
		assert.Nil(t, foundTrace)
		assert.NotEmpty(t, diff)
	}
}
