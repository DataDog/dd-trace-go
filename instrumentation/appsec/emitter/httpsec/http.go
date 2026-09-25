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
	"sync"

	// Blank import needed to use embed for the default blocked response payloads
	_ "embed"
	"net/http"
	"net/netip"
	"sync/atomic"

	"github.com/DataDog/go-libddwaf/v5"

	"github.com/DataDog/dd-trace-go/v2/appsec/events"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/dyngo"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/waf/actions"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/waf/addresses"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/trace"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/emitter/waf"
	"github.com/DataDog/dd-trace-go/v2/internal/clientip"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	telemetrylog "github.com/DataDog/dd-trace-go/v2/internal/telemetry/log"
)

// HandlerOperation type representing an HTTP operation. It must be created with
// StartOperation() and finished with its Finish().
type (
	HandlerOperation struct {
		dyngo.Operation
		*waf.ContextOperation

		// wafContextOwner indicates if the waf.ContextOperation was started by us or not and if we need to close it.
		wafContextOwner bool

		// framework is the name of the framework or library that started the operation.
		framework string
		// method is the HTTP method for the current handler operation.
		method string
		// route is the HTTP route for the current handler operation (or the URL if no route is available).
		route string

		// downstreamRequestBodyAnalysis is the number of times a call to a downstream request body monitoring function was made.
		downstreamRequestBodyAnalysis atomic.Int32

		// blockAction stores the latest HTTP response-replacing action.
		blockAction atomic.Pointer[actions.BlockHTTP]

		// downstreamRequestOverrides holds behavioral overrides for future downstream requests, related
		// to a redirect chain.
		downstreamRequestOverrides   map[string]DownstreamRequestOverride
		downstreamRequestOverridesMu sync.Mutex
	}

	DownstreamRequestOverride struct {
		DownstreamURL       string
		AnalyzeBody         bool
		OriginalRequestBody libddwaf.Encodable
	}

	// HandlerOperationArgs is the HTTP handler operation arguments.
	HandlerOperationArgs struct {
		Framework    string // Optional: name of the framework or library being used
		Method       string
		RequestURI   string
		RequestRoute string
		Host         string
		RemoteAddr   string
		ClientIP     netip.Addr
		Headers      map[string][]string
		Cookies      map[string][]string
		QueryParams  map[string][]string
		PathParams   map[string]string
	}

	// HandlerOperationRes is the HTTP handler operation results.
	HandlerOperationRes struct {
		Headers    map[string][]string
		StatusCode int
	}

	// EarlyBlock is used to trigger an early block before the handler is executed.
	EarlyBlock struct{}
)

func (HandlerOperationArgs) IsArgOf(*HandlerOperation)   {}
func (HandlerOperationRes) IsResultOf(*HandlerOperation) {}

// clientIdentity returns who the client is, according to the caller when it
// knows, and to the default policy otherwise.
//
// Callers reaching AppSec through instrumentation/httptrace have already
// resolved this and pass it down; those wrapping a handler directly have not.
func clientIdentity(opts *Config, r *http.Request) netip.Addr {
	// A header named by DD_TRACE_CLIENT_IP_HEADER outranks a supplied address:
	// see clientip.CustomHeaderConfigured.
	if !clientip.CustomHeaderConfigured() && opts.ClientIP.IsValid() {
		return opts.ClientIP
	}
	_, clientIP := clientip.Resolve(r.Header, true, r.RemoteAddr)
	return clientIP
}

func StartOperation(ctx context.Context, args HandlerOperationArgs, span trace.TagSetter) (*HandlerOperation, *atomic.Pointer[actions.BlockHTTP], context.Context) {
	wafOp, found := dyngo.FindOperation[waf.ContextOperation](ctx)
	if !found {
		wafOp, ctx = waf.StartContextOperation(ctx, span)
	}

	op := &HandlerOperation{
		Operation:        dyngo.NewOperation(wafOp),
		ContextOperation: wafOp,
		wafContextOwner:  !found, // If we started the parent operation, we finish it, otherwise we don't
		framework:        args.Framework,
		method:           args.Method,
		route:            args.RequestRoute,
	}

	// We need to use an atomic pointer to store the action because the action may be created asynchronously in the future.
	dyngo.OnData(op, func(a *actions.BlockHTTP) {
		op.blockAction.Store(a)
	})

	dyngo.OnData(op, func(evt DownstreamRequestOverride) {
		op.downstreamRequestOverridesMu.Lock()
		defer op.downstreamRequestOverridesMu.Unlock()

		if op.downstreamRequestOverrides == nil {
			op.downstreamRequestOverrides = make(map[string]DownstreamRequestOverride, 1)
		}
		op.downstreamRequestOverrides[evt.DownstreamURL] = evt
	})

	return op, &op.blockAction, dyngo.StartAndRegisterOperation(ctx, op, args)
}

// Framework returns the name of the framework or library that started the operation.
func (op *HandlerOperation) Framework() string {
	return op.framework
}

// Method returns the HTTP method for the current handler operation.
func (op *HandlerOperation) Method() string {
	return op.method
}

// Route returns the HTTP route for the current handler operation.
func (op *HandlerOperation) Route() string {
	return op.route
}

// DownstreamRequestBodyAnalysis returns the number of times a call to a downstream request body monitoring function was made.
func (op *HandlerOperation) DownstreamRequestBodyAnalysis() int {
	return int(op.downstreamRequestBodyAnalysis.Load())
}

// HasDownstreamRequestOverride checks if a downstream request override exists for the given URL,
// meaning it is part of a redirect chain.
func (op *HandlerOperation) HasDownstreamRequestOverride(url string) bool {
	op.downstreamRequestOverridesMu.Lock()
	defer op.downstreamRequestOverridesMu.Unlock()
	_, ok := op.downstreamRequestOverrides[url]
	return ok
}

// ConsumeDownstreamRequestOverride consumes and removes a downstream request override for the given
// URL, returning the override data.
func (op *HandlerOperation) ConsumeDownstreamRequestOverride(url string) (DownstreamRequestOverride, bool) {
	op.downstreamRequestOverridesMu.Lock()
	defer op.downstreamRequestOverridesMu.Unlock()
	override, ok := op.downstreamRequestOverrides[url]
	delete(op.downstreamRequestOverrides, url)
	return override, ok
}

// IncrementDownstreamRequestBodyAnalysis increments the number of times a call to a downstream request body monitoring function was made.
func (op *HandlerOperation) IncrementDownstreamRequestBodyAnalysis() {
	op.downstreamRequestBodyAnalysis.Add(1)
}

// Finish completes the HTTP operation and its request context. A tracked block
// action must be applied before Finish; otherwise it is reported as failed.
func (op *HandlerOperation) Finish(res HandlerOperationRes) {
	dyngo.FinishOperation(op, res)
	// Direct callers cannot apply an action that the response-data WAF run in
	// dyngo.FinishOperation produces before finishContext submits request
	// telemetry. An action applied earlier has already consumed its handler.
	op.reportBlockFailure(op.blockAction.Load())
	op.finishContext()
}

func (op *HandlerOperation) finishContext() {
	if op.wafContextOwner {
		op.ContextOperation.Finish()
	}
}

const (
	monitorParsedBodyErrorLog = `
"appsec: parsed http body monitoring ignored: could not find the http handler instrumentation metadata in the request context:
	the request handler is not being monitored by a middleware function or the provided context is not the expected request context
`
	monitorResponseBodyErrorLog = `
"appsec: http response body monitoring ignored: could not find the http handler instrumentation metadata in the request context:
	the request handler is not being monitored by a middleware function or the provided context is not the expected request context
`
)

// MonitorParsedBody starts and finishes the SDK body operation.
// This function should not be called when AppSec is disabled in order to
// get more accurate error logs.
func MonitorParsedBody(ctx context.Context, body any) error {
	return waf.RunSimple(ctx,
		addresses.NewAddressesBuilder().
			WithRequestBody(body).
			Build(),
		monitorParsedBodyErrorLog,
	)
}

// MonitorResponseBody gets the response body through the in-app WAF.
// This function should not be called when AppSec is disabled in order to get
// more accurate error logs.
func MonitorResponseBody(ctx context.Context, body any) error {
	return waf.RunSimple(ctx,
		addresses.NewAddressesBuilder().
			WithResponseBody(body).
			Build(),
		monitorResponseBodyErrorLog,
	)
}

// Return the map of parsed cookies if any and following the specification of
// the rule address `server.request.cookies`.
func makeCookies(parsed []*http.Cookie) map[string][]string {
	if len(parsed) == 0 {
		return nil
	}
	cookies := make(map[string][]string, len(parsed))
	for _, c := range parsed {
		cookies[c.Name] = append(cookies[c.Name], c.Value)
	}
	return cookies
}

// RouteMatched can be called if BeforeHandle is started too early in the http request lifecycle like
// before the router has matched the request to a route. This can happen when the HTTP handler is wrapped
// using http.NewServeMux instead of http.WrapHandler. In this case the route is empty and so are the path parameters.
// In this case the route and path parameters will be filled in later by calling RouteMatched with the actual route.
// If RouteMatched returns an error, the request should be considered blocked and the error should be reported.
func RouteMatched(ctx context.Context, route string, routeParams map[string]string) error {
	op, ok := dyngo.FindOperation[HandlerOperation](ctx)
	if !ok {
		log.Debug("appsec: RouteMatched called without an active HandlerOperation in the context, ignoring")
		telemetrylog.With(telemetry.WithTags([]string{"product:appsec"})).
			Warn("appsec: RouteMatched called without an active HandlerOperation in the context, ignoring")
		return nil
	}

	// Overwrite the previous route that was created using a quantization algorithm
	op.route = route

	var err error
	dyngo.OnData(op, func(e *events.BlockingSecurityEvent) {
		err = e
	})

	// Call the WAF with this new data
	op.Run(op, addresses.NewAddressesBuilder().
		WithPathParams(routeParams).
		Build(),
	)

	return err
}

func responseStarted(w http.ResponseWriter) bool {
	if res, ok := w.(interface{ Committed() bool }); ok {
		return res.Committed()
	}
	if res, ok := w.(interface{ Written() bool }); ok {
		return res.Written()
	}
	res, ok := w.(interface{ Status() int })
	return ok && res.Status() != 0
}

// applyBlockAction consumes action and resolves the block outcome it reports.
// It returns true when the protected handler must not run, which includes the
// case of a response that can no longer be replaced.
func applyBlockAction(op *HandlerOperation, action *actions.BlockHTTP, w http.ResponseWriter, r *http.Request, onBlock []func()) bool {
	if action == nil || action.Handler == nil {
		return false
	}

	if op.ContextOperation.BlockingUnavailable() {
		op.reportBlockFailure(action)
		return false
	}

	if responseStarted(w) {
		op.reportBlockFailure(action)
		// The response can no longer be replaced, but the protected handler
		// must still be interrupted to prevent application side effects.
		for _, f := range onBlock {
			f()
		}
		return true
	}

	handler := action.Handler
	action.Handler = nil
	// The handler is consumed, so no later caller can report this block. Report
	// it as failed unless its response is delivered, also when a block callback
	// or the block response panics.
	delivered := false
	defer func() {
		if !delivered {
			op.blockFailed()
		}
	}()

	for _, f := range onBlock {
		f()
	}

	handler.ServeHTTP(w, r)
	if actions.CommitBlockResponse(w) != nil {
		return true
	}
	delivered = true
	op.blockApplied()
	op.ContextOperation.SetRequestBlocked()
	return true
}

// reportBlockFailure consumes an action that was never applied and reports that
// its response could not be enforced.
func (op *HandlerOperation) reportBlockFailure(action *actions.BlockHTTP) {
	if action == nil || action.Handler == nil {
		return
	}
	action.Handler = nil
	op.blockFailed()
}

func (op *HandlerOperation) blockFailed() {
	if metrics := op.ContextOperation.GetMetricsInstance(); metrics != nil {
		metrics.SetBlockFailed()
	}
}

// blockApplied reports that a block response was delivered to the client.
func (op *HandlerOperation) blockApplied() {
	if metrics := op.ContextOperation.GetMetricsInstance(); metrics != nil {
		metrics.SetBlockApplied()
	}
}

// BeforeHandle contains the appsec functionality that should be executed before a http.Handler runs.
// It returns the modified http.ResponseWriter and http.Request, an additional afterHandle function
// that should be executed after the Handler runs, and a handled bool that instructs the caller not to run
// the original handler. handled can also be true when a block was requested after the response was committed
// and AppSec could not replace that response.
func BeforeHandle(
	w http.ResponseWriter,
	r *http.Request,
	span trace.TagSetter,
	opts *Config,
) (http.ResponseWriter, *http.Request, func(), bool) {
	if opts == nil {
		opts = defaultWrapHandlerConfig
	}
	if opts.ResponseHeaderCopier == nil {
		opts.ResponseHeaderCopier = defaultWrapHandlerConfig.ResponseHeaderCopier
	}

	clientIP := clientIdentity(opts, r)

	op, blockAtomic, ctx := StartOperation(r.Context(), HandlerOperationArgs{
		Framework:    opts.Framework,
		Method:       r.Method,
		RequestURI:   r.RequestURI,
		RequestRoute: opts.Route,
		Host:         r.Host,
		RemoteAddr:   r.RemoteAddr,
		ClientIP:     clientIP,
		Headers:      r.Header,
		Cookies:      makeCookies(r.Cookies()),
		QueryParams:  r.URL.Query(),
		PathParams:   opts.RouteParams,
	}, span)
	tr := r.WithContext(ctx)

	afterHandle := func() {
		var statusCode int
		if res, ok := w.(interface{ Status() int }); ok {
			statusCode = res.Status()
		}

		// Finishing the HTTP operation can produce a blocking action from the
		// response data. Apply or reject that action before finishing the WAF
		// context, which submits waf.requests.
		dyngo.FinishOperation(op, HandlerOperationRes{
			Headers:    opts.ResponseHeaderCopier(w),
			StatusCode: statusCode,
		})
		defer op.finishContext()

		applyBlockAction(op, blockAtomic.Load(), w, tr, opts.OnBlock)
	}

	handled := applyBlockAction(op, blockAtomic.Load(), w, tr, opts.OnBlock)

	// We register a handler for cases that would require us to write the blocking response before any more code
	// from a specific framework (like Gin) is executed that would write another (wrong) response here.
	dyngo.OnData(op, func(EarlyBlock) {
		applyBlockAction(op, blockAtomic.Load(), w, tr, opts.OnBlock)
	})

	return w, tr, afterHandle, handled
}

// WrapHandler wraps the given HTTP handler with the abstract HTTP operation defined by HandlerOperationArgs and
// HandlerOperationRes.
// The onBlock params are used to cleanup the context when needed.
// It is a specific patch meant for Gin, for which we must abort the
// context since it uses a queue of handlers and it's the only way to make
// sure other queued handlers don't get executed.
// TODO: this patch must be removed/improved when we rework our actions/operations system
func WrapHandler(handler http.Handler, span trace.TagSetter, opts *Config) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tw, tr, afterHandle, handled := BeforeHandle(w, r, span, opts)
		defer afterHandle()
		if handled {
			return
		}

		handler.ServeHTTP(tw, tr)
	})
}
