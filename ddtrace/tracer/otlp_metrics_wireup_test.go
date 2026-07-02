// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package tracer

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/DataDog/datadog-go/v5/statsd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	tinternal "github.com/DataDog/dd-trace-go/v2/ddtrace/tracer/internal"
	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
)

// captureMetricsServer records every POST body it receives.
type captureMetricsServer struct {
	mu     sync.Mutex
	bodies [][]byte
	*httptest.Server
}

func newCaptureMetricsServer(t *testing.T) *captureMetricsServer {
	t.Helper()
	cs := &captureMetricsServer{}
	cs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		cs.mu.Lock()
		cs.bodies = append(cs.bodies, b)
		cs.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(cs.Server.Close)
	return cs
}

func (cs *captureMetricsServer) receivedBodies() [][]byte {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	out := make([][]byte, len(cs.bodies))
	copy(out, cs.bodies)
	return out
}

// TestOTLPMetricsConcentratorRoutesToExporter verifies that a concentrator wired
// with an otlpMetricsExporter routes flushed stats to the OTLP endpoint and does
// not use the agent's native /v0.6/stats path.
func TestOTLPMetricsConcentratorRoutesToExporter(t *testing.T) {
	otlpSrv := newCaptureMetricsServer(t)

	dt := newDummyTransport()
	cfg, err := newTestConfig(withNoopInfoHTTPClient(), func(c *config) {
		c.ddTransport = dt
		c.internalConfig.SetEnv("prod", internalconfig.OriginCode)
	})
	require.NoError(t, err)

	bucketSize := int64(500_000)
	c := newConcentrator(cfg, bucketSize, &statsd.NoOpClientDirect{})
	c.otlpExporter = &otlpMetricsExporter{
		transport: newOTLPTransport(otlpSrv.Server.Client(), otlpSrv.URL+"/v1/metrics", nil),
		protocol:  "http/json",
		cfg:       cfg.internalConfig,
	}

	s := &Span{
		name:     "http.request",
		service:  "test-svc",
		resource: "/api/v1",
		// 30 seconds in the past ensures its bucket is always before the flush window.
		start:    time.Now().UnixNano() - int64(30*time.Second),
		duration: int64(50 * time.Millisecond),
		metrics:  map[string]float64{keyMeasured: 1},
	}
	ss, ok := c.newTracerStatSpan(s, nil)
	require.True(t, ok)

	// Add the span and flush directly, bypassing goroutine channels to keep the
	// test deterministic.
	c.add(ss)
	c.flushAndSend(time.Now(), withCurrentBucket)

	bodies := otlpSrv.receivedBodies()
	require.NotEmpty(t, bodies, "OTLP endpoint must receive at least one payload")

	// Verify the payload is valid JSON with a resourceMetrics array.
	var parsed map[string]any
	require.NoError(t, json.Unmarshal(bodies[0], &parsed), "OTLP metrics payload must be valid JSON")
	rm, ok := parsed["resourceMetrics"].([]any)
	require.True(t, ok, "expected resourceMetrics array in OTLP payload")
	require.NotEmpty(t, rm)

	// The native /v0.6/stats path must not have been used.
	assert.Empty(t, dt.Stats(), "native stats path must not be used when otlpExporter is set")
}

// TestOTLPSpanMetricsHeaderOnNativeTraces verifies that when OTLP span metrics are
// enabled the Datadog-Client-Computed-Stats: yes header is present on native trace
// payloads so the agent does not recompute stats.
func TestOTLPSpanMetricsHeaderOnNativeTraces(t *testing.T) {
	var headerValue string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/info" {
			// No /v0.6/stats endpoint, so CanComputeStats stays false.
			// The header must still be set via OTLPSpanMetricsEnabled.
			w.Write([]byte(`{"endpoints":[]}`))
			return
		}
		headerValue = r.Header.Get("Datadog-Client-Computed-Stats")
	}))
	defer srv.Close()

	trc, err := newTracer(
		WithAgentAddr(srv.Listener.Addr().String()),
		func(c *config) {
			c.internalConfig.SetOTLPSpanMetricsEnabled(true, internalconfig.OriginCode)
		},
	)
	require.NoError(t, err)
	setGlobalTracer(trc)
	defer trc.Stop()

	p, err := encode(getTestTrace(1, 1))
	require.NoError(t, err)
	_, err = trc.config.ddTransport.send(p)
	require.NoError(t, err)

	assert.Equal(t, "yes", headerValue, "Datadog-Client-Computed-Stats must be 'yes' when OTLPSpanMetricsEnabled")
}

// TestOTLPConcentratorHTTPRouteAttribute verifies that a span carrying the OTel
// http.route tag (ext.HTTPRoute) produces a data-point with http.route as a
// first-class attribute (FR06). This exercises the ext.HTTPRoute fallback path
// in newTracerStatSpan, which populates ClientGroupedStats.HTTPEndpoint so that
// buildDataPointAttributes emits it — without relying on the peer-tags path.
func TestOTLPConcentratorHTTPRouteAttribute(t *testing.T) {
	otlpSrv := newCaptureMetricsServer(t)

	cfg, err := newTestConfig(withNoopInfoHTTPClient())
	require.NoError(t, err)

	bucketSize := int64(500_000)
	c := newConcentrator(cfg, bucketSize, &statsd.NoOpClientDirect{})
	c.otlpExporter = &otlpMetricsExporter{
		transport: newOTLPTransport(otlpSrv.Server.Client(), otlpSrv.URL+"/v1/metrics", nil),
		protocol:  "http/json",
		cfg:       cfg.internalConfig,
	}
	c.otlpPeerTags = otlpDefaultPeerTags

	s := &Span{
		name:     "web.request",
		service:  "svc",
		resource: "web.request",
		start:    time.Now().UnixNano() - int64(30*time.Second),
		duration: int64(50 * time.Millisecond),
		metrics:  map[string]float64{keyMeasured: 1},
		meta:     tinternal.NewSpanMetaFromMap(map[string]string{"http.route": "/users/{id}"}),
	}
	ss, ok := c.newTracerStatSpan(s, nil)
	require.True(t, ok)
	c.add(ss)
	c.flushAndSend(time.Now(), withCurrentBucket)

	bodies := otlpSrv.receivedBodies()
	require.NotEmpty(t, bodies, "OTLP endpoint must receive a payload")

	body := string(bodies[0])
	assert.Contains(t, body, `"http.route"`, "http.route key must appear as a data-point attribute")
	assert.Contains(t, body, `/users/{id}`, "http.route value must appear in the payload")
}

// TestOTLPTraceWriterStatsComputedResourceAttr verifies that _dd.stats_computed=true
// is added to the OTLP trace resource when OTLP span metrics are enabled (FR15).
func TestOTLPTraceWriterStatsComputedResourceAttr(t *testing.T) {
	t.Run("present-when-enabled", func(t *testing.T) {
		cfg, err := newTestConfig(func(c *config) {
			c.internalConfig.SetOTLPSpanMetricsEnabled(true, internalconfig.OriginCode)
		})
		require.NoError(t, err)

		w := newOTLPTraceWriter(cfg)
		var found bool
		for _, kv := range w.resource.Attributes {
			if kv.Key == "_dd.stats_computed" {
				found = true
				assert.Equal(t, "true", kv.Value.GetStringValue(), "_dd.stats_computed must be string \"true\"")
			}
		}
		assert.True(t, found, "_dd.stats_computed attribute must be present when OTLPSpanMetricsEnabled")
	})

	t.Run("absent-when-disabled", func(t *testing.T) {
		cfg, err := newTestConfig(func(c *config) {
			c.internalConfig.SetOTLPSpanMetricsEnabled(false, internalconfig.OriginCode)
		})
		require.NoError(t, err)

		w := newOTLPTraceWriter(cfg)
		for _, kv := range w.resource.Attributes {
			assert.NotEqual(t, "_dd.stats_computed", kv.Key,
				"_dd.stats_computed must not be present when OTLPSpanMetricsEnabled is false")
		}
	})
}
