// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package wrap

import (
	"fmt"
	"math"
	"net/http"
	"os"
	"strconv"

	"github.com/DataDog/dd-trace-go/contrib/net/http/v2/internal/config"
	"github.com/DataDog/dd-trace-go/v2/appsec/events"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/baggage"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/httpsec"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/httptrace"
)

type AfterRoundTrip = func(*http.Response, error) (*http.Response, error)

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
		tracer.Tag(ext.HTTPURL, httptrace.URLFromRequest(req, cfg.QueryString)),
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

	// if RASP is enabled, check whether the request is supposed to be blocked.
	if config.Instrumentation.AppSecRASPEnabled() {
		if err := httpsec.ProtectRoundTrip(ctx, req.URL.String()); err != nil {
			span.Finish() // Finish the span as we're blocking the request...
			return nil, nil, err
		}
	}

	after := func(resp *http.Response, err error) (*http.Response, error) {
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
				span.SetTag(ext.Error, fmt.Errorf("%d: %s", resp.StatusCode, http.StatusText(resp.StatusCode)))
			}
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
