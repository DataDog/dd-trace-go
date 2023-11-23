// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package listener

import (
	"fmt"
	"sync"
	"time"

	"github.com/DataDog/appsec-internal-go/limiter"
	waf "github.com/DataDog/go-libddwaf/v2"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo/instrumentation/httpsec/emitter"
	sharedsec "gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo/instrumentation/sharedsec/emitter"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo/instrumentation/sharedsec/listener"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/samplernames"
)

// HTTP rule addresses currently supported by the WAF
const (
	ServerRequestMethodAddr            = "server.request.method"
	ServerRequestRawURIAddr            = "server.request.uri.raw"
	ServerRequestHeadersNoCookiesAddr  = "server.request.headers.no_cookies"
	ServerRequestCookiesAddr           = "server.request.cookies"
	ServerRequestQueryAddr             = "server.request.query"
	ServerRequestPathParamsAddr        = "server.request.path_params"
	ServerRequestBodyAddr              = "server.request.body"
	ServerResponseStatusAddr           = "server.response.status"
	ServerResponseHeadersNoCookiesAddr = "server.response.headers.no_cookies"
	HTTPClientIPAddr                   = "http.client_ip"
	UserIDAddr                         = "usr.id"
)

// List of HTTP rule addresses currently supported by the WAF
var supportedpAddresses = map[string]struct{}{
	ServerRequestMethodAddr:            {},
	ServerRequestRawURIAddr:            {},
	ServerRequestHeadersNoCookiesAddr:  {},
	ServerRequestCookiesAddr:           {},
	ServerRequestQueryAddr:             {},
	ServerRequestPathParamsAddr:        {},
	ServerRequestBodyAddr:              {},
	ServerResponseStatusAddr:           {},
	ServerResponseHeadersNoCookiesAddr: {},
	HTTPClientIPAddr:                   {},
	UserIDAddr:                         {},
}

func SupportsAddress(addr string) bool {
	_, ok := supportedpAddresses[addr]
	return ok
}

// NewWAFEventListener returns the WAF event listener to register in order to enable it.
func NewWAFEventListener(handle *waf.Handle, actions sharedsec.Actions, addresses map[string]struct{}, timeout time.Duration, limiter limiter.Limiter) dyngo.EventListener {
	var monitorRulesOnce sync.Once // per instantiation
	// TODO: port wafDiags to telemetry metrics and logs instead of span tags (ultimately removing them from here hopefully)
	wafDiags := handle.Diagnostics()

	return emitter.OnHandlerOperationStart(func(op *emitter.Operation, args emitter.HandlerOperationArgs) {
		wafCtx := waf.NewContext(handle)

		if wafCtx == nil {
			// The WAF event listener got concurrently released
			return
		}

		if _, ok := addresses[UserIDAddr]; ok {
			// OnUserIDOperationStart happens when appsec.SetUser() is called. We run the WAF and apply actions to
			// see if the associated user should be blocked. Since we don't control the execution flow in this case
			// (SetUser is SDK), we delegate the responsibility of interrupting the handler to the user.
			op.On(sharedsec.OnUserIDOperationStart(func(operation *sharedsec.UserIDOperation, args sharedsec.UserIDOperationArgs) {
				wafResult := listener.RunWAF(wafCtx, waf.RunAddressData{Persistent: map[string]any{UserIDAddr: args.UserID}}, timeout)
				if wafResult.HasActions() || wafResult.HasEvents() {
					listener.ProcessHTTPSDKAction(operation, actions, wafResult.Actions)
					listener.AddSecurityEvents(op, limiter, wafResult.Events)
					log.Debug("appsec: WAF detected a suspicious user: %s", args.UserID)
				}
			}))
		}

		values := make(map[string]any, 7)
		for addr := range addresses {
			switch addr {
			case HTTPClientIPAddr:
				if args.ClientIP.IsValid() {
					values[HTTPClientIPAddr] = args.ClientIP.String()
				}
			case ServerRequestMethodAddr:
				values[ServerRequestMethodAddr] = args.Method
			case ServerRequestRawURIAddr:
				values[ServerRequestRawURIAddr] = args.RequestURI
			case ServerRequestHeadersNoCookiesAddr:
				if headers := args.Headers; headers != nil {
					values[ServerRequestHeadersNoCookiesAddr] = headers
				}
			case ServerRequestCookiesAddr:
				if cookies := args.Cookies; cookies != nil {
					values[ServerRequestCookiesAddr] = cookies
				}
			case ServerRequestQueryAddr:
				if query := args.Query; query != nil {
					values[ServerRequestQueryAddr] = query
				}
			case ServerRequestPathParamsAddr:
				if pathParams := args.PathParams; pathParams != nil {
					values[ServerRequestPathParamsAddr] = pathParams
				}
			}
		}

		wafResult := listener.RunWAF(wafCtx, waf.RunAddressData{Persistent: values}, timeout)
		if wafResult.HasActions() || wafResult.HasEvents() {
			interrupt := listener.ProcessActions(op, actions, wafResult.Actions)
			listener.AddSecurityEvents(op, limiter, wafResult.Events)
			log.Debug("appsec: WAF detected an attack before executing the request")
			if interrupt {
				wafCtx.Close()
				return
			}
		}

		if _, ok := addresses[ServerRequestBodyAddr]; ok {
			op.On(emitter.OnSDKBodyOperationStart(func(sdkBodyOp *emitter.SDKBodyOperation, args emitter.SDKBodyOperationArgs) {
				wafResult := listener.RunWAF(wafCtx, waf.RunAddressData{Persistent: map[string]any{ServerRequestBodyAddr: args.Body}}, timeout)
				if wafResult.HasActions() || wafResult.HasEvents() {
					listener.ProcessHTTPSDKAction(sdkBodyOp, actions, wafResult.Actions)
					listener.AddSecurityEvents(op, limiter, wafResult.Events)
					log.Debug("appsec: WAF detected a suspicious request body")
				}
			}))
		}

		op.On(emitter.OnHandlerOperationFinish(func(op *emitter.Operation, res emitter.HandlerOperationRes) {
			defer wafCtx.Close()

			values := make(map[string]any, 2)
			if _, ok := addresses[ServerResponseStatusAddr]; ok {
				// serverResponseStatusAddr is a string address, so we must format the status code...
				values[ServerResponseStatusAddr] = fmt.Sprintf("%d", res.Status)
			}

			if _, ok := addresses[ServerResponseHeadersNoCookiesAddr]; ok && res.Headers != nil {
				values[ServerResponseHeadersNoCookiesAddr] = res.Headers
			}

			// Run the WAF, ignoring the returned actions - if any - since blocking after the request handler's
			// response is not supported at the moment.
			wafResult := listener.RunWAF(wafCtx, waf.RunAddressData{Persistent: values}, timeout)

			// Add WAF metrics.
			overallRuntimeNs, internalRuntimeNs := wafCtx.TotalRuntime()
			listener.AddWAFMonitoringTags(op, wafDiags.Version, overallRuntimeNs, internalRuntimeNs, wafCtx.TotalTimeouts())

			// Add the following metrics once per instantiation of a WAF handle
			monitorRulesOnce.Do(func() {
				listener.AddRulesMonitoringTags(op, &wafDiags)
				op.AddTag(ext.ManualKeep, samplernames.AppSec)
			})

			// Log the attacks if any
			if wafResult.HasEvents() {
				log.Debug("appsec: attack detected by the waf")
				listener.AddSecurityEvents(op, limiter, wafResult.Events)
			}
		}))
	})
}
