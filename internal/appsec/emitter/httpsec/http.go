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

	// Blank import needed to use embed for the default blocked response payloads
	_ "embed"
	"net/http"
	"strings"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/dyngo/domain"
	"gopkg.in/DataDog/dd-trace-go.v1/dyngo/event/httpevent"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/sharedsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/trace"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/trace/httptrace"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"

	"github.com/DataDog/appsec-internal-go/netip"
)

func init() {
	domain.HTTP.Activate()
}

// WrapHandler wraps the given HTTP handler with the abstract HTTP operation defined by HandlerOperationArgs and
// HandlerOperationRes.
// The onBlock params are used to cleanup the context when needed.
// It is a specific patch meant for Gin, for which we must abort the
// context since it uses a queue of handlers and it's the only way to make
// sure other queued handlers don't get executed.
// TODO: this patch must be removed/improved when we rework our actions/operations system
func WrapHandler(handler http.Handler, span ddtrace.Span, pathParams map[string]string, opts *Config) http.Handler {
	if opts == nil {
		opts = defaultWrapHandlerConfig
	} else if opts.ResponseHeaderCopier == nil {
		opts.ResponseHeaderCopier = defaultWrapHandlerConfig.ResponseHeaderCopier
	}

	trace.SetAppSecEnabledTags(span)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ipTags, clientIP := httptrace.ClientIPTags(r.Header, true, r.RemoteAddr)
		log.Debug("appsec: http client ip detection returned `%s` given the http headers `%v`", clientIP, r.Header)
		trace.SetTags(span, ipTags)

		var bypassHandler http.Handler
		var blocking bool
		args := MakeHandlerOperationArgs(r, clientIP, pathParams)
		ctx, op := httpevent.StartHandlerOperation(r.Context(), args, func(op *httpevent.HandlerOperation) {
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
				for _, f := range opts.OnBlock {
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
			setResponseHeadersTags(span, opts.ResponseHeaderCopier(w))
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
func MakeHandlerOperationArgs(r *http.Request, clientIP netip.Addr, pathParams map[string]string) httpevent.HandlerOperationArgs {
	cookies := makeCookies(r) // TODO(Julio-Guerra): avoid actively parsing the cookies thanks to dynamic instrumentation
	headers := headersRemoveCookies(r.Header)
	headers["host"] = []string{r.Host}
	return httpevent.HandlerOperationArgs{
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
func MakeHandlerOperationRes(w http.ResponseWriter) httpevent.HandlerOperationRes {
	var status int
	if mw, ok := w.(interface{ Status() int }); ok {
		status = mw.Status()
	}
	return httpevent.HandlerOperationRes{Status: status, Headers: headersRemoveCookies(w.Header())}
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
