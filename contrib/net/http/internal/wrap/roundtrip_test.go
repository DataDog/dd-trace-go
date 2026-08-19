// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package wrap

import (
	"fmt"
	"math"
	"net/http"
	"net/url"
	"testing"

	"github.com/DataDog/dd-trace-go/contrib/net/http/v2/internal/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
)

func TestObserveRoundTripSemanticTags(t *testing.T) {
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
			name:          "OTel unknown method error with explicit port",
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
			name:        "legacy error with explicit port",
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

func TestRoundTripperServerAddressPort(t *testing.T) {
	tests := []struct {
		name        string
		target      string
		host        string
		wantAddress string
		wantPort    int
	}{
		{name: "URL authority", target: "http://example.com:8080/path", wantAddress: "example.com", wantPort: 8080},
		{name: "implicit HTTP port", target: "http://example.com/path", wantAddress: "example.com", wantPort: 80},
		{name: "implicit HTTPS port", target: "https://example.com/path", wantAddress: "example.com", wantPort: 443},
		{name: "implicit non-HTTP port", target: "ftp://example.com/path", wantAddress: "example.com", wantPort: -1},
		{name: "IPv6", target: "http://[fe80::1%25eth0]:8080/path", wantAddress: "fe80::1%eth0", wantPort: 8080},
		{name: "IPv6 implicit port", target: "http://[2000::1]/path", wantAddress: "2000::1", wantPort: 80},
		{name: "Host authority", target: "http://1.2.3.4:8080/path", host: "example.com:9090", wantAddress: "example.com", wantPort: 9090},
		{name: "zero port", target: "http://example.com:0/path", wantAddress: "example.com", wantPort: 0},
		{name: "malformed Host authority", target: "http://example.com/path", host: "bad::authority", wantAddress: "bad::authority", wantPort: -1},
		{name: "invalid Host authority port", target: "http://example.com/path", host: "example.com:invalid", wantAddress: "example.com", wantPort: -1},
		{name: "out-of-range port", target: "http://example.com:65536/path", wantAddress: "example.com", wantPort: -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target, err := url.Parse(tt.target)
			require.NoError(t, err)
			req := &http.Request{URL: target, Host: tt.host}
			address, port := serverAddressPort(req)
			assert.Equal(t, tt.wantAddress, address)
			assert.Equal(t, tt.wantPort, port)
		})
	}
}
