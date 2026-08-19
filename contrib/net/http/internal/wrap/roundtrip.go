// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package wrap

import (
	"crypto/tls"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/http/httptrace"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/DataDog/dd-trace-go/contrib/net/http/v2/internal/config"

	"github.com/DataDog/dd-trace-go/v2/appsec/events"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/baggage"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/httpsec"
	instrumentationhttptrace "github.com/DataDog/dd-trace-go/v2/instrumentation/httptrace"
)

type AfterRoundTrip = func(*http.Response, error) (*http.Response, error)

// httpTraceTimings captures key timing events from httptrace.ClientTrace.
type httpTraceTimings struct {
	mu                         sync.Mutex
	dnsStart, dnsEnd           time.Time // +checklocks:mu
	connectStart, connectEnd   time.Time // +checklocks:mu
	tlsStart, tlsEnd           time.Time // +checklocks:mu
	getConnStart, gotConn      time.Time // +checklocks:mu
	wroteHeaders, gotFirstByte time.Time // +checklocks:mu
	connectErr                 error     // +checklocks:mu
	tlsErr                     error     // +checklocks:mu
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
	t.mu.Lock()
	defer t.mu.Unlock()

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
		DNSStart: func(httptrace.DNSStartInfo) {
			timings.mu.Lock()
			timings.dnsStart = time.Now()
			timings.mu.Unlock()
		},
		DNSDone: func(httptrace.DNSDoneInfo) {
			timings.mu.Lock()
			timings.dnsEnd = time.Now()
			timings.mu.Unlock()
		},
		ConnectStart: func(network, addr string) {
			timings.mu.Lock()
			timings.connectStart = time.Now()
			timings.mu.Unlock()
		},
		ConnectDone: func(network, addr string, err error) {
			timings.mu.Lock()
			timings.connectEnd = time.Now()
			timings.connectErr = err
			timings.mu.Unlock()
		},
		TLSHandshakeStart: func() {
			timings.mu.Lock()
			timings.tlsStart = time.Now()
			timings.mu.Unlock()
		},
		TLSHandshakeDone: func(_ tls.ConnectionState, err error) {
			timings.mu.Lock()
			timings.tlsEnd = time.Now()
			timings.tlsErr = err
			timings.mu.Unlock()
		},
		GetConn: func(hostPort string) {
			timings.mu.Lock()
			timings.getConnStart = time.Now()
			timings.mu.Unlock()
		},
		GotConn: func(httptrace.GotConnInfo) {
			timings.mu.Lock()
			timings.gotConn = time.Now()
			timings.mu.Unlock()
		},
		WroteHeaders: func() {
			timings.mu.Lock()
			timings.wroteHeaders = time.Now()
			timings.mu.Unlock()
		},
		GotFirstResponseByte: func() {
			timings.mu.Lock()
			timings.gotFirstByte = time.Now()
			timings.mu.Unlock()
		},
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
	opts := []tracer.StartSpanOption{
		tracer.SpanType(ext.SpanTypeHTTP),
		tracer.ResourceName(resourceName),
		tracer.Tag(ext.Component, config.ComponentName),
		tracer.Tag(ext.SpanKind, ext.SpanKindClient),
	}
	if cfg.OTelSemanticsEnabled {
		method, _, originalMethod := config.NormalizeClientRequestMethod(req)
		opts = append(opts,
			tracer.Tag(ext.HTTPRequestMethod, method),
			tracer.Tag(ext.URLFull, instrumentationhttptrace.URLFullFromClientRequest(req, cfg.QueryString)),
		)
		if originalMethod != "" {
			opts = append(opts, tracer.Tag(ext.HTTPRequestMethodOriginal, originalMethod))
		}
		address, port := serverAddressPort(req)
		opts = append(opts, tracer.Tag(ext.ServerAddress, address))
		if port != -1 {
			opts = append(opts, tracer.Tag(ext.ServerPort, port))
		}
	} else {
		opts = append(opts,
			tracer.Tag(ext.HTTPMethod, req.Method),
			tracer.Tag(ext.HTTPURL, instrumentationhttptrace.URLFromClientRequest(req, cfg.QueryString)),
			tracer.Tag(ext.NetworkDestinationName, req.URL.Hostname()),
		)
		if port, err := strconv.Atoi(req.URL.Port()); err == nil {
			opts = append(opts, tracer.Tag(ext.NetworkDestinationPort, port))
		}
	}
	if !math.IsNaN(cfg.AnalyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, cfg.AnalyticsRate))
	}
	if cfg.ServiceName != "" {
		opts = append(opts, instrumentation.ServiceNameWithSource(cfg.ServiceName, cfg.ServiceSource))
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
			statusCode := strconv.Itoa(resp.StatusCode)
			if cfg.OTelSemanticsEnabled {
				span.SetTag(ext.HTTPResponseStatusCode, statusCode)
			} else {
				span.SetTag(ext.HTTPCode, statusCode)
			}
			if cfg.IsStatusError(resp.StatusCode) {
				span.SetTag("http.errors", resp.Status)
				span.SetTag(ext.ErrorNoStackTrace, fmt.Errorf("%d: %s", resp.StatusCode, http.StatusText(resp.StatusCode)))
				if cfg.OTelSemanticsEnabled {
					span.SetTag(ext.ErrorType, statusCode)
				}
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

// serverAddressPort returns OpenTelemetry `server.address` and `server.port` from
// `req`'s effective request authority (https://www.rfc-editor.org/rfc/rfc9110.html#name-host-and-authority).
//
// The effective authority is: URL host/port for absolute requests, otherwise the authority header (https://github.com/open-telemetry/semantic-conventions/blob/v1.44.0/docs/http/http-spans.md?plain=1#L203-L215).
// This method seems to implement the opposite by preferring the authority field.
// That's because [http.DefaultTransport] overrides the URL's host/port with the
// non-empty authority field, even for absolute-form request targets.
// So the effective authority is always the authority field when present.
//
// The authority is not the network address, which is covered by `network.peer.*`.
//
// Port is inferred from HTTP schemes ("80" or "443") when absent from the effective
// authority (https://github.com/open-telemetry/semantic-conventions/blob/v1.44.0/docs/non-normative/http-migration.md?plain=1#L79);
// explicit but invalid ports or non-HTTP schemes return port -1.
func serverAddressPort(req *http.Request) (address string, port int) {
	authority := req.Host
	if authority == "" {
		authority = req.URL.Host
	}
	return parseAuthority(authority, req.URL.Scheme)
}

func parseAuthority(authority, scheme string) (address string, port int) {
	// We need to do two things with the `authority` string:
	// 1. Split it into host and port.
	// 2. Clean up possible IPv6 hosts by removing brackets.
	//
	// `net.SplitHostPort` does almost that, but errs on authorities without port.
	//
	// We chose to check for the presence of a port before calling `SplitHostPort`,
	// instead of calling it and handling the error, since the complexity is similar:
	// both approaches require IPv6 bracket detection.

	// Look for `:`, but don't conflate it with IPv6 delimiters.
	if strings.HasPrefix(authority, "[") {
		if strings.HasSuffix(authority, "]") {
			return authority[1 : len(authority)-1], defaultPort(scheme)
		}
	} else if !strings.Contains(authority, ":") {
		return authority, defaultPort(scheme)
	}

	address, portString, err := net.SplitHostPort(authority)
	if err != nil {
		return authority, -1
	}
	// Check if the port is an integer and, as a bonus, within the valid range (0-65535).
	parsedPort, err := strconv.ParseUint(portString, 10, 16)
	if err != nil {
		return address, -1
	}
	return address, int(parsedPort)
}

// The inferred default port from HTTP schemes.
func defaultPort(scheme string) int {
	switch {
	case strings.EqualFold(scheme, "http"):
		return 80
	case strings.EqualFold(scheme, "https"):
		return 443
	default:
		return -1
	}
}

func identityAfterRoundTrip(resp *http.Response, err error) (*http.Response, error) {
	return resp, err
}
