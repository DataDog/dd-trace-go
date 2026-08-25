// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package httptrace

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	appsectrace "github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/trace"
)

func TestAppSecSpanTagSetter(t *testing.T) {
	for _, tt := range []struct {
		name string
		otel bool
		want map[string]any
	}{
		{
			name: "Datadog",
			want: map[string]any{
				ext.HTTPClientIP:    "203.0.113.10",
				ext.NetworkClientIP: "192.0.2.1",
				"appsec.custom":     "value",
			},
		},
		{
			name: "OpenTelemetry",
			otel: true,
			want: map[string]any{
				ext.ClientAddress:      "203.0.113.10",
				ext.NetworkPeerAddress: "192.0.2.1",
				"appsec.custom":        "value",
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tags := appsectrace.TestTagSetter{}
			setter := AppSecSpanTagSetter(tags, tt.otel)
			setter.SetTag(ext.HTTPClientIP, "203.0.113.10")
			setter.SetTag(ext.NetworkClientIP, "192.0.2.1")
			setter.SetTag("appsec.custom", "value")

			assert.Equal(t, tt.want, map[string]any(tags))
		})
	}
}
