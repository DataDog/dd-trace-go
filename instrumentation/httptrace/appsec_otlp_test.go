// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package httptrace

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	otlpcommon "go.opentelemetry.io/proto/otlp/common/v1"
	otlptrace "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"

	"github.com/DataDog/go-libddwaf/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec"
	"github.com/DataDog/dd-trace-go/v2/internal/samplernames"
)

// TestAppSecEventsOverOTLP checks that App and API Protection sends its events as the
// _dd.appsec.json attribute when the tracer exports traces over OTLP, even if the agent
// reports meta_struct support. OTLP has no meta_struct field.
func TestAppSecEventsOverOTLP(t *testing.T) {
	// TracesData has the same wire format as the OTLP ExportTraceServiceRequest, and it does not
	// add the dependencies of the OTLP collector package to the modules of this repository.
	exports := make(chan *otlptrace.TracesData, 10)
	intake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/info":
			if err := json.NewEncoder(w).Encode(map[string]any{
				"span_meta_structs": true,
				"endpoints":         []string{"/v0.4/traces"},
			}); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
		case "/v1/traces":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			req := &otlptrace.TracesData{}
			if err := proto.Unmarshal(body, req); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			exports <- req
		default:
			_, _ = io.Copy(io.Discard, r.Body)
			_, _ = w.Write([]byte("{}"))
		}
	}))
	defer intake.Close()

	t.Setenv("DD_TRACE_AGENT_URL", intake.URL)
	t.Setenv("OTEL_TRACES_EXPORTER", "otlp")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", intake.URL+"/v1/traces")
	t.Setenv("DD_APPSEC_ENABLED", "true")

	if wafOK, err := libddwaf.Usable(); !wafOK {
		t.Skipf("App and API Protection is not available in this build: %v", err)
	}

	require.NoError(t, tracer.Start(tracer.WithLogStartup(false)))
	t.Cleanup(tracer.Stop)
	// The package config records the App and API Protection state at startup once. Reset it, so
	// that later tests do not see App and API Protection as enabled at startup.
	t.Cleanup(ResetCfg)
	if !appsec.Enabled() {
		t.Skip("App and API Protection is not available in this build")
	}

	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rw, _, afterHandle, handled := BeforeHandle(nil, w, r)
		defer afterHandle()
		if handled {
			return
		}
		_, _ = rw.Write([]byte("ok"))
	}))
	defer app.Close()

	// A security scanner User-Agent triggers the WAF rule ua0-600-7xx.
	req, err := http.NewRequest(http.MethodGet, app.URL+"/hello?q=<script>alert(1)</script>", nil)
	require.NoError(t, err)
	req.Header.Set("User-Agent", "sqlmap/1.7")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()

	tracer.Stop()

	var export *otlptrace.TracesData
	select {
	case export = <-exports:
	case <-time.After(5 * time.Second):
		t.Fatal("no OTLP payload received")
	}

	var serverSpan *otlptrace.Span
	for _, rs := range export.GetResourceSpans() {
		for _, ss := range rs.GetScopeSpans() {
			for _, s := range ss.GetSpans() {
				if s.GetKind() == otlptrace.Span_SPAN_KIND_SERVER {
					serverSpan = s
				}
			}
		}
	}
	require.NotNil(t, serverSpan, "no server span in the OTLP payload")

	attrs := make(map[string]*otlpcommon.AnyValue, len(serverSpan.GetAttributes()))
	for _, kv := range serverSpan.GetAttributes() {
		attrs[kv.GetKey()] = kv.GetValue()
	}

	require.Contains(t, attrs, "_dd.appsec.json")
	var events struct {
		Triggers []struct {
			Rule struct {
				ID string `json:"id"`
			} `json:"rule"`
		} `json:"triggers"`
	}
	require.NoError(t, json.Unmarshal([]byte(attrs["_dd.appsec.json"].GetStringValue()), &events))
	require.NotEmpty(t, events.Triggers)
	ruleIDs := make([]string, 0, len(events.Triggers))
	for _, trigger := range events.Triggers {
		ruleIDs = append(ruleIDs, trigger.Rule.ID)
	}
	assert.Contains(t, ruleIDs, "ua0-600-7xx")

	assert.NotContains(t, attrs, "appsec")
	require.Contains(t, attrs, "_sampling_priority_v1")
	assert.EqualValues(t, ext.PriorityUserKeep, attrs["_sampling_priority_v1"].GetDoubleValue())
	assert.Equal(t, samplernames.AppSec.DecisionMaker(), attrs["_dd.p.dm"].GetStringValue())
}
