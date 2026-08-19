// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package wrap

import (
	"fmt"
	"math"
	"net/http"
	"testing"

	"github.com/DataDog/dd-trace-go/contrib/net/http/v2/internal/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
)

func TestObserveRoundTripEmitsModeSpecificHTTPAttributes(t *testing.T) {
	tests := []struct {
		name          string
		otelSemantics bool
		method        string
		target        string
		statusCode    int
		statusError   bool
		wantTags      map[string]any
		absentTags    []string
	}{
		{
			name:          "OTel mode emits semantic attributes and omits legacy attributes",
			otelSemantics: true,
			method:        "PROPFIND",
			target:        "http://example.com:8080/path?key=value",
			statusCode:    http.StatusInternalServerError,
			statusError:   true,
			wantTags: map[string]any{
				ext.HTTPRequestMethod:         "_OTHER",
				ext.HTTPRequestMethodOriginal: "PROPFIND",
				ext.URLFull:                   "http://example.com:8080/path?key=value",
				ext.ServerAddress:             "example.com",
				ext.ServerPort:                float64(8080),
				ext.HTTPResponseStatusCode:    "500",
				ext.ErrorType:                 "500",
			},
			absentTags: []string{ext.HTTPMethod, ext.HTTPCode},
		},
		{
			name:        "legacy mode emits Datadog attributes and omits semantic attributes",
			method:      http.MethodGet,
			target:      "http://example.com:8080/path",
			statusCode:  http.StatusBadRequest,
			statusError: true,
			wantTags: map[string]any{
				ext.HTTPMethod:             http.MethodGet,
				ext.HTTPURL:                "http://example.com:8080/path",
				ext.NetworkDestinationName: "example.com",
				ext.NetworkDestinationPort: float64(8080),
				ext.HTTPCode:               "400",
			},
			absentTags: []string{ext.HTTPRequestMethod, ext.HTTPResponseStatusCode},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			cfg := &config.RoundTripperConfig{
				CommonConfig: config.CommonConfig{
					AnalyticsRate: math.NaN(),
					IgnoreRequest: func(*http.Request) bool { return false },
					ResourceNamer: func(*http.Request) string { return "resource" },
					IsStatusError: func(int) bool { return tt.statusError },
				},
				SpanNamer:            func(*http.Request) string { return "http.request" },
				QueryString:          true,
				OTelSemanticsEnabled: tt.otelSemantics,
			}
			req, err := http.NewRequest(tt.method, tt.target, nil)
			require.NoError(t, err)
			_, after, err := ObserveRoundTrip(cfg, req)
			require.NoError(t, err)

			status := fmt.Sprintf("%d %s", tt.statusCode, http.StatusText(tt.statusCode))
			_, err = after(&http.Response{StatusCode: tt.statusCode, Status: status}, nil)
			require.NoError(t, err)
			spans := mt.FinishedSpans()
			require.Len(t, spans, 1)
			for tag, want := range tt.wantTags {
				assert.Equal(t, want, spans[0].Tag(tag), tag)
			}
			for _, tag := range tt.absentTags {
				assert.Nil(t, spans[0].Tag(tag), tag)
			}
		})
	}
}
