// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package otelc holds the otelc rules and hooks for net/http. It is the otelc
// counterpart of ../orchestrion.client.yml and ../orchestrion.server.yml.
package otelc

import (
	"net"
	"net/http"

	"go.opentelemetry.io/otelc/pkg/hook"

	nethttptrace "github.com/DataDog/dd-trace-go/contrib/net/http/v2/compiletime"
)

// Parameter indices as otelc numbers them: a method's receiver is 0 and the
// declared parameters follow.
const (
	roundTripRequestParam = 1

	roundTripResponseResult = 0
	roundTripErrorResult    = 1
)

// BeforeServe wraps the server's handler, the port of the Server.Serve aspect.
// ListenAndServe, ListenAndServeTLS and http.Serve all funnel through
// (*Server).Serve, so hooking it covers them too.
func BeforeServe(_ hook.HookContext, srv *http.Server, _ net.Listener) {
	if srv == nil {
		return
	}
	if srv.Handler == nil {
		srv.Handler = nethttptrace.WrapHandler(http.DefaultServeMux)
		return
	}
	srv.Handler = nethttptrace.WrapHandler(srv.Handler)
}

// BeforeRoundTrip starts the client span and swaps in the request carrying the
// propagation headers, the port of the Transport.RoundTrip aspect.
func BeforeRoundTrip(ctx hook.HookContext, transport *http.Transport, req *http.Request) {
	if req == nil || isTracerInternal(transport) {
		return
	}

	traced, after, err := nethttptrace.ObserveRoundTrip(req)
	if err != nil {
		// Orchestrion returns (nil, err) without reaching the transport.
		ctx.SetSkipCall(true)
		ctx.SetData(&roundTripState{err: err})
		return
	}

	ctx.SetParam(roundTripRequestParam, traced)
	ctx.SetData(&roundTripState{after: after})
}

// AfterRoundTrip finishes the span BeforeRoundTrip started.
func AfterRoundTrip(ctx hook.HookContext, resp *http.Response, err error) {
	state, ok := ctx.GetData().(*roundTripState)
	if !ok || state == nil {
		return
	}

	if state.err != nil {
		ctx.SetReturnVal(roundTripResponseResult, nil)
		ctx.SetReturnVal(roundTripErrorResult, state.err)
		return
	}

	// The response is handed back to the caller, who closes it.
	//nolint:bodyclose
	resp, err = state.after(resp, err)
	ctx.SetReturnVal(roundTripResponseResult, resp)
	ctx.SetReturnVal(roundTripErrorResult, err)
}

// roundTripState carries what BeforeRoundTrip produced across to
// AfterRoundTrip. A typed struct rather than SetKeyData keeps this to one small
// allocation per request.
type roundTripState struct {
	after nethttptrace.AfterRoundTrip
	err   error
}
