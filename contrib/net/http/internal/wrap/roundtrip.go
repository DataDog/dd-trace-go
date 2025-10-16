// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package wrap

import (
	"crypto/tls"
	"fmt"
	"math"
	"net/http"
	"net/http/httptrace"
	"os"
	"strconv"
	"time"

	"github.com/DataDog/dd-trace-go/contrib/net/http/v2/internal/config"
	"github.com/DataDog/dd-trace-go/v2/appsec/events"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/baggage"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/httpsec"
	instrumentationhttptrace "github.com/DataDog/dd-trace-go/v2/instrumentation/httptrace"
)

type AfterRoundTrip = func(*http.Response, error) (*http.Response, error)

// httpTraceTimings captures key timing events from httptrace.ClientTrace
type httpTraceTimings struct {
	dnsStart, dnsEnd           time.Time
	connectStart, connectEnd   time.Time
	tlsStart, tlsEnd           time.Time
	getConnStart, gotConn      time.Time
	wroteHeaders, gotFirstByte time.Time
	connectErr                 error
	tlsErr                     error
}

// addDurationTag adds a timing tag to the span if both timestamps are valid
func (t *httpTraceTimings) addDurationTag(span *tracer.Span, tagName string, start, end time.Time) {
	if !start.IsZero() && !end.IsZero() {
		duration := float64(end.Sub(start).Nanoseconds()) / 1e6
		span.SetTag(tagName, duration)
	}
}

// addTimingTags adds all timing information to the span
func (t *httpTraceTimings) addTimingTags(span *tracer.Span) {
	t.addDurationTag(span, "http.dns.duration_ms", t.dnsStart, t.dnsEnd)
	t.addDurationTag(span, "http.connect.duration_ms", t.connectStart, t.connectEnd)
	t.addDurationTag(span, "http.tls.duration_ms", t.tlsStart, t.tlsEnd)
	t.addDurationTag(span, "http.get_conn.duration_ms", t.getConnStart, t.gotConn)
	t.addDurationTag(span, "http.first_byte.duration_ms", t.wroteHeaders, t.gotFirstByte)

	// Add error information if present
	if t.connectErr != nil {
		span.SetTag("http.connect.error", t.connectErr.Error())
	}
	if t.tlsErr != nil {
		span.SetTag("http.tls.error", t.tlsErr.Error())
	}
}

// newClientTrace creates a ClientTrace that captures timing information
func newClientTrace(timings *httpTraceTimings) *httptrace.ClientTrace {
	return &httptrace.ClientTrace{
		DNSStart:             func(httptrace.DNSStartInfo) { timings.dnsStart = time.Now() },
		DNSDone:              func(httptrace.DNSDoneInfo) { timings.dnsEnd = time.Now() },
		ConnectStart:         func(network, addr string) { timings.connectStart = time.Now() },
		ConnectDone:          func(network, addr string, err error) { timings.connectEnd = time.Now(); timings.connectErr = err },
		TLSHandshakeStart:    func() { timings.tlsStart = time.Now() },
		TLSHandshakeDone:     func(_ tls.ConnectionState, err error) { timings.tlsEnd = time.Now(); timings.tlsErr = err },
		GetConn:              func(hostPort string) { timings.getConnStart = time.Now() },
		GotConn:              func(httptrace.GotConnInfo) { timings.gotConn = time.Now() },
		WroteHeaders:         func() { timings.wroteHeaders = time.Now() },
		GotFirstResponseByte: func() { timings.gotFirstByte = time.Now() },
	}
}

// ObserveRoundTrip performs actions before the base [http.RoundTripper.RoundTrip] using the
// provided [*config.RoundTripperConfig] (which cannot be nil). It returns the possibly modified
// [*http.Request] and a function to be called after the base [http.RoundTripper.RoundTrip] function
// is executed, and before returning control to the caller.
//
// If RASP features are enabled, an error will be returned if the request should be blocked, in
// which case the caller must immediately abort the [http.RoundTripper.RoundTrip] and forward the
// error as-is. An error is never returned in RASP features are not enabled.
func ObserveRoundTrip(cfg *config.RoundTripperConfig, req *http.Request) (*http.Request, AfterRoundTrip, error) {
	if cfg.IgnoreRequest(req) {
		return req, identityAfterRoundTrip, nil
	}

	resourceName := cfg.ResourceNamer(req)
	spanName := cfg.SpanNamer(req)
	// Make a copy of the URL so we don't modify the outgoing request
	url := *req.URL
	url.User = nil // Do not include userinfo in the HTTPURL tag.
	opts := []tracer.StartSpanOption{
		tracer.SpanType(ext.SpanTypeHTTP),
		tracer.ResourceName(resourceName),
		tracer.Tag(ext.HTTPMethod, req.Method),
		tracer.Tag(ext.HTTPURL, instrumentationhttptrace.URLFromRequest(req, cfg.QueryString)),
		tracer.Tag(ext.Component, config.ComponentName),
		tracer.Tag(ext.SpanKind, ext.SpanKindClient),
		tracer.Tag(ext.NetworkDestinationName, url.Hostname()),
	}
	if !math.IsNaN(cfg.AnalyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, cfg.AnalyticsRate))
	}
	if cfg.ServiceName != "" {
		opts = append(opts, tracer.ServiceName(cfg.ServiceName))
	}
	if port, err := strconv.Atoi(url.Port()); err == nil {
		opts = append(opts, tracer.Tag(ext.NetworkDestinationPort, port))
	}
	if len(cfg.SpanOpts) > 0 {
		opts = append(opts, cfg.SpanOpts...)
	}

	// Start a new span
	span, ctx := tracer.StartSpanFromContext(req.Context(), spanName, opts...)

	// Apply the before hook, if any
	if cfg.Before != nil {
		cfg.Before(req, span)
	}

	// Setup ClientTrace for detailed timing if enabled
	var timings *httpTraceTimings
	if cfg.ClientTimings {
		timings = &httpTraceTimings{}
		ctx = httptrace.WithClientTrace(ctx, newClientTrace(timings))
	}

	// Clone the request so we can modify it without causing visible side-effects to the caller...
	req = req.Clone(ctx)
	for k, v := range baggage.All(ctx) {
		span.SetBaggageItem(k, v)
	}
	if cfg.Propagation {
		// inject the span context into the http request copy
		err := tracer.Inject(span.Context(), tracer.HTTPHeadersCarrier(req.Header))
		if err != nil {
			// this should never happen
			fmt.Fprintf(os.Stderr, "contrib/net/http.Roundtrip: failed to inject http headers: %s\n", err.Error())
		}
	}

	var afterAppsec func(*http.Response)

	// if RASP is enabled, check whether the request is supposed to be blocked.
	if config.Instrumentation.AppSecRASPEnabled() {
		var err error
		afterAppsec, err = httpsec.ProtectRoundTrip(ctx, req)
		if err != nil {
			span.Finish() // Finish the span as we're blocking the request...
			return nil, nil, err
		}
	}

	after := func(resp *http.Response, err error) (*http.Response, error) {
		if afterAppsec != nil {
			afterAppsec(resp)
		}

		// Register http errors and observe the status code...
		if err != nil {
			span.SetTag("http.errors", err.Error())
			if cfg.ErrCheck == nil || cfg.ErrCheck(err) {
				span.SetTag(ext.Error, err)
			}
		} else {
			span.SetTag(ext.HTTPCode, strconv.Itoa(resp.StatusCode))
			if cfg.IsStatusError(resp.StatusCode) {
				span.SetTag("http.errors", resp.Status)
				span.SetTag(ext.ErrorNoStackTrace, fmt.Errorf("%d: %s", resp.StatusCode, http.StatusText(resp.StatusCode)))
			}
		}

		if cfg.ClientTimings && timings != nil {
			timings.addTimingTags(span)
		}

		// Run the after hooks & finish the span
		if cfg.After != nil {
			cfg.After(resp, span)
		}
		if !events.IsSecurityError(err) && (cfg.ErrCheck == nil || cfg.ErrCheck(err)) {
			span.Finish(tracer.WithError(err))
		} else {
			span.Finish()
		}

		// Finally, forward the response and error back to the caller
		return resp, err
	}

	return req, after, nil
}

func identityAfterRoundTrip(resp *http.Response, err error) (*http.Response, error) {
	return resp, err
}
