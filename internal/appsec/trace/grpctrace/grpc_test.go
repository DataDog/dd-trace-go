// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package grpctrace

import (
	"fmt"
	"testing"

	testlib "gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/_testlib"

	"github.com/stretchr/testify/require"
)

func TestTags(t *testing.T) {
	for _, eventCase := range []struct {
		name          string
		events        []any
		expectedTag   string
		expectedError bool
	}{
		{
			name:   "no-event",
			events: nil,
		},
		{
			name:        "one-event",
			events:      []any{"one"},
			expectedTag: `{"triggers":["one"]}`,
		},
		{
			name:        "two-events",
			events:      []any{"one", "two"},
			expectedTag: `{"triggers":["one","two"]}`,
		},
	} {
		eventCase := eventCase
		for _, metadataCase := range []struct {
			name         string
			md           map[string][]string
			expectedTags map[string]interface{}
		}{
			{
				name: "zero-metadata",
			},
			{
				name: "xff-metadata",
				md: map[string][]string{
					"x-forwarded-for": {"1.2.3.4", "4.5.6.7"},
					":authority":      {"something"},
				},
				expectedTags: map[string]interface{}{
					"grpc.metadata.x-forwarded-for": "1.2.3.4,4.5.6.7",
				},
			},
			{
				name: "xff-metadata",
				md: map[string][]string{
					"x-forwarded-for": {"1.2.3.4"},
					":authority":      {"something"},
				},
				expectedTags: map[string]interface{}{
					"grpc.metadata.x-forwarded-for": "1.2.3.4",
				},
			},
			{
				name: "no-monitored-metadata",
				md: map[string][]string{
					":authority": {"something"},
				},
			},
		} {
			metadataCase := metadataCase
			t.Run(fmt.Sprintf("%s-%s", eventCase.name, metadataCase.name), func(t *testing.T) {
				var span testlib.MockSpan
				err := setSecurityEventsTags(&span, eventCase.events)
				if eventCase.expectedError {
					require.Error(t, err)
					return
				}
				require.NoError(t, err)
				SetRequestMetadataTags(&span, metadataCase.md)

				if eventCase.events != nil {
					testlib.RequireContainsMapSubset(t, span.Tags, map[string]interface{}{
						"_dd.appsec.json": eventCase.expectedTag,
						"manual.keep":     true,
						"appsec.event":    true,
						"_dd.origin":      "appsec",
					})
				}

				if l := len(metadataCase.expectedTags); l > 0 {
					testlib.RequireContainsMapSubset(t, span.Tags, metadataCase.expectedTags)
				}

				require.False(t, span.Finished)
			})
		}
	}
}
