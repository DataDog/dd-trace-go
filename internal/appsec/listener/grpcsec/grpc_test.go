// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package grpcsec

import (
	"fmt"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/listener/waf"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/samplernames"

	"github.com/stretchr/testify/require"
)

type MockSpan struct {
	Tags map[string]any
}

func (m *MockSpan) SetTag(key string, value interface{}) {
	if m.Tags == nil {
		m.Tags = make(map[string]any)
	}
	if key == ext.ManualKeep {
		if value == samplernames.AppSec {
			m.Tags[ext.ManualKeep] = true
		}
	} else {
		m.Tags[key] = value
	}
}

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
				var span MockSpan
				err := waf.SetEventSpanTags(&span, eventCase.events)
				if eventCase.expectedError {
					require.Error(t, err)
					return
				}
				require.NoError(t, err)
				SetRequestMetadataTags(&span, metadataCase.md)

				if eventCase.events != nil {
					require.Subset(t, span.Tags, map[string]interface{}{
						"_dd.appsec.json": eventCase.expectedTag,
						"manual.keep":     true,
						"appsec.event":    true,
						"_dd.origin":      "appsec",
					})
				}

				if l := len(metadataCase.expectedTags); l > 0 {
					require.Subset(t, span.Tags, metadataCase.expectedTags)
				}
			})
		}
	}
}
