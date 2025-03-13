// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

//generator:ignore-build-constraint
//go:build !buildtag

package ddspan

import (
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

func (*TestCase) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name": "test.root",
			},
			Children: trace.Traces{
				{
					Tags: map[string]any{
						"name": "spanFromHTTPRequest",
					},
					Meta: map[string]string{
						"function-name": "spanFromHTTPRequest",
						"foo":           "bar",
					},
					Children: trace.Traces{
						{
							Tags: map[string]any{
								"name": "tagSpecificSpan",
							},
							Meta: map[string]string{
								"function-name": "tagSpecificSpan",
								"variant":       "notag",
							},
						},
					},
				},
			},
		},
	}
}

//dd:span variant:notag
func tagSpecificSpan() (string, error) {
	return "Variant NoTag", nil
}
