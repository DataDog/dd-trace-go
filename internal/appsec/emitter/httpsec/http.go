// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package httpsec defines is the HTTP instrumentation API and contract for
// AppSec. It defines an abstract representation of HTTP handlers, along with
// helper functions to wrap (aka. instrument) standard net/http handlers.
// HTTP integrations must use this package to enable AppSec features for HTTP,
// which listens to this package's operation events.
package httpsec

import (
	"context"
	// Blank import needed to use embed for the default blocked response payloads
	_ "embed"
	"net/http"
	"strings"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/httpsec/types"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/sharedsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/listener"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/trace"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/trace/httptrace"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"

	"github.com/DataDog/appsec-internal-go/netip"
)

// MonitorParsedBody starts and finishes the SDK body operation.
// This function should not be called when AppSec is disabled in order to
// get preciser error logs.
func MonitorParsedBody(ctx context.Context, body any) error {
	parent := fromContext(ctx)
	if parent == nil {
		log.Error("appsec: parsed http body monitoring ignored: could not find the http handler instrumentation metadata in the request context: the request handler is not being monitored by a middleware function or the provided context is not the expected request context")
		return nil
	}

	return ExecuteSDKBodyOperation(parent, types.SDKBodyOperationArgs{Body: body})
}

// ExecuteSDKBodyOperation starts and finishes the SDK Body operation by emitting a dyngo start and finish events
// An error is returned if the body associated to that operation must be blocked
func ExecuteSDKBodyOperation(parent dyngo.Operation, args types.SDKBodyOperationArgs) error {
	var err error
	op := &types.SDKBodyOperation{Operation: dyngo.NewOperation(parent)}
	dyngo.OnData(op, func(e error) {
		err = e
	})
	dyngo.StartOperation(op, args)
	dyngo.FinishOperation(op, types.SDKBodyOperationRes{})
	return err
}

// WrapHandler wraps the given HTTP handler with the abstract HTTP operation defined by HandlerOperationArgs and
// HandlerOperationRes.
// The onBlock params are used to cleanup the context when needed.
// It is a specific patch meant for Gin, for which we must abort the
// context since it uses a queue of handlers and it's the only way to make
// sure other queued handlers don't get executed.
// TODO: this patch must be removed/improved when we rework our actions/operations system
func WrapHandler(handler http.Handler, span ddtrace.Span, pathParams map[string]string, onBlock ...func()) http.Handler {
	trace.SetAppSecEnabledTags(span)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ipTags, clientIP := httptrace.ClientIPTags(r.Header, true, r.RemoteAddr)
		log.Debug("appsec: http client ip detection returned `%s` given the http headers `%v`", clientIP, r.Header)
		trace.SetTags(span, ipTags)

		var bypassHandler http.Handler
		var blocking bool
		args := MakeHandlerOperationArgs(r, clientIP, pathParams)
		ctx, op := StartOperation(r.Context(), args, func(op *types.Operation) {
			dyngo.OnData(op, func(a *sharedsec.Action) {
				bypassHandler = a.HTTP()
				blocking = a.Blocking()
			})
		})
		r = r.WithContext(ctx)

		defer func() {
			events := op.Finish(MakeHandlerOperationRes(w))

			// Execute the onBlock functions to make sure blocking works properly
			// in case we are instrumenting the Gin framework
			if blocking {
				op.SetTag(trace.BlockedRequestTag, true)
				for _, f := range onBlock {
					f()
				}
			}

			if bypassHandler != nil {
				bypassHandler.ServeHTTP(w, r)
			}

			// Add the request headers span tags out of args.Headers instead of r.Header as it was normalized and some
			// extra headers have been added such as the Host header which is removed from the original Go request headers
			// map
			setRequestHeadersTags(span, args.Headers)
			setResponseHeadersTags(span, w.Header())
			trace.SetTags(span, op.Tags())
			if len(events) > 0 {
				httptrace.SetSecurityEventsTags(span, events)
			}
		}()

		if bypassHandler != nil {
			handler = bypassHandler
			bypassHandler = nil
		}
		handler.ServeHTTP(w, r)
	})
}

// MakeHandlerOperationArgs creates the HandlerOperationArgs value.
func MakeHandlerOperationArgs(r *http.Request, clientIP netip.Addr, pathParams map[string]string) types.HandlerOperationArgs {
	cookies := makeCookies(r) // TODO(Julio-Guerra): avoid actively parsing the cookies thanks to dynamic instrumentation
	headers := headersRemoveCookies(r.Header)
	headers["host"] = []string{r.Host}
	return types.HandlerOperationArgs{
		Method:     r.Method,
		RequestURI: r.RequestURI,
		Headers:    headers,
		Cookies:    cookies,
		Query:      r.URL.Query(), // TODO(Julio-Guerra): avoid actively parsing the query values thanks to dynamic instrumentation
		PathParams: pathParams,
		ClientIP:   clientIP,
	}
}

// MakeHandlerOperationRes creates the HandlerOperationRes value.
func MakeHandlerOperationRes(w http.ResponseWriter) types.HandlerOperationRes {
	var status int
	if mw, ok := w.(interface{ Status() int }); ok {
		status = mw.Status()
	}
	return types.HandlerOperationRes{Status: status, Headers: headersRemoveCookies(w.Header())}
}

// Remove cookies from the request headers and return the map of headers
// Used from `server.request.headers.no_cookies` and server.response.headers.no_cookies` addresses for the WAF
func headersRemoveCookies(headers http.Header) map[string][]string {
	headersNoCookies := make(http.Header, len(headers))
	for k, v := range headers {
		k := strings.ToLower(k)
		if k == "cookie" {
			continue
		}
		headersNoCookies[k] = v
	}
	return headersNoCookies
}

// Return the map of parsed cookies if any and following the specification of
// the rule address `server.request.cookies`.
func makeCookies(r *http.Request) map[string][]string {
	parsed := r.Cookies()
	if len(parsed) == 0 {
		return nil
	}
	cookies := make(map[string][]string, len(parsed))
	for _, c := range parsed {
		cookies[c.Name] = append(cookies[c.Name], c.Value)
	}
	return cookies
}

// StartOperation starts an HTTP handler operation, along with the given
// context and arguments and emits a start event up in the operation stack.
// The operation is linked to the global root operation since an HTTP operation
// is always expected to be first in the operation stack.
func StartOperation(ctx context.Context, args types.HandlerOperationArgs, setup ...func(*types.Operation)) (context.Context, *types.Operation) {
	op := &types.Operation{
		Operation:  dyngo.NewOperation(nil),
		TagsHolder: trace.NewTagsHolder(),
	}
	newCtx := context.WithValue(ctx, listener.ContextKey{}, op)
	for _, cb := range setup {
		cb(op)
	}
	dyngo.StartOperation(op, args)
	return newCtx, op
}

// fromContext returns the Operation object stored in the context, if any
func fromContext(ctx context.Context) *types.Operation {
	// Avoid a runtime panic in case of type-assertion error by collecting the 2 return values
	op, _ := ctx.Value(listener.ContextKey{}).(*types.Operation)
	return op
}
