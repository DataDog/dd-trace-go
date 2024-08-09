// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package httpsec

import (
	"fmt"
	"math/rand"
	"sync"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/config"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/httpsec/types"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/sharedsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/listener"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/listener/ossec"
	shared "gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/listener/sharedsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/listener/sqlsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/samplernames"

	"github.com/DataDog/appsec-internal-go/limiter"
	waf "github.com/DataDog/go-libddwaf/v3"
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
	ServerIoNetURLAddr                 = "server.io.net.url"
)

// List of HTTP rule addresses currently supported by the WAF
var supportedAddresses = listener.AddressSet{
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
	ServerIoNetURLAddr:                 {},
	ossec.ServerIOFSFileAddr:           {},
	sqlsec.ServerDBStatementAddr:       {},
	sqlsec.ServerDBTypeAddr:            {},
}

// Install registers the HTTP WAF Event Listener on the given root operation.
func Install(wafHandle *waf.Handle, cfg *config.Config, lim limiter.Limiter, root dyngo.Operation) {
	if listener := newWafEventListener(wafHandle, cfg, lim); listener != nil {
		log.Debug("appsec: registering the HTTP WAF Event Listener")
		dyngo.On(root, listener.onEvent)
	}
}

type wafEventListener struct {
	wafHandle *waf.Handle
	config    *config.Config
	addresses listener.AddressSet
	limiter   limiter.Limiter
	wafDiags  waf.Diagnostics
	once      sync.Once
}

// newWAFEventListener returns the WAF event listener to register in order to enable it.
func newWafEventListener(wafHandle *waf.Handle, cfg *config.Config, limiter limiter.Limiter) *wafEventListener {
	if wafHandle == nil {
		log.Debug("appsec: no WAF Handle available, the HTTP WAF Event Listener will not be registered")
		return nil
	}

	addresses := listener.FilterAddressSet(supportedAddresses, wafHandle)
	if len(addresses) == 0 {
		log.Debug("appsec: no supported HTTP address is used by currently loaded WAF rules, the HTTP WAF Event Listener will not be registered")
		return nil
	}

	return &wafEventListener{
		wafHandle: wafHandle,
		config:    cfg,
		addresses: addresses,
		limiter:   limiter,
		wafDiags:  wafHandle.Diagnostics(),
	}
}

func (l *wafEventListener) onEvent(op *types.Operation, args types.HandlerOperationArgs) {
	wafCtx, err := l.wafHandle.NewContextWithBudget(l.config.WAFTimeout)
	if err != nil {
		log.Debug("appsec: could not create budgeted WAF context: %v", err)
	}
	// Early return in the following cases:
	// - wafCtx is nil, meaning it was concurrently released
	// - err is not nil, meaning context creation failed
	if wafCtx == nil || err != nil {
		// The WAF event listener got concurrently released
		return
	}

	if SSRFAddressesPresent(l.addresses) {
		dyngo.On(op, shared.MakeWAFRunListener(&op.SecurityEventsHolder, wafCtx, l.limiter, func(args types.RoundTripOperationArgs) waf.RunAddressData {
			return waf.RunAddressData{Ephemeral: map[string]any{ServerIoNetURLAddr: args.URL}}
		}))
	}

	if ossec.OSAddressesPresent(l.addresses) {
		ossec.RegisterOpenListener(op, &op.SecurityEventsHolder, wafCtx, l.limiter)
	}

	if sqlsec.SQLAddressesPresent(l.addresses) {
		sqlsec.RegisterSQLListener(op, &op.SecurityEventsHolder, wafCtx, l.limiter)
	}

	if _, ok := l.addresses[UserIDAddr]; ok {
		// OnUserIDOperationStart happens when appsec.SetUser() is called. We run the WAF and apply actions to
		// see if the associated user should be blocked. Since we don't control the execution flow in this case
		// (SetUser is SDK), we delegate the responsibility of interrupting the handler to the user.
		dyngo.On(op, shared.MakeWAFRunListener(&op.SecurityEventsHolder, wafCtx, l.limiter, func(args sharedsec.UserIDOperationArgs) waf.RunAddressData {
			return waf.RunAddressData{Persistent: map[string]any{UserIDAddr: args.UserID}}
		}))
	}

	values := make(map[string]any, 8)
	for addr := range l.addresses {
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

	wafResult := shared.RunWAF(wafCtx, waf.RunAddressData{Persistent: values})
	if wafResult.HasActions() || wafResult.HasEvents() {
		interrupt := shared.ProcessActions(op, wafResult.Actions)
		shared.AddSecurityEvents(&op.SecurityEventsHolder, l.limiter, wafResult.Events)
		log.Debug("appsec: WAF detected an attack before executing the request")
		if interrupt {
			wafCtx.Close()
			return
		}
	}

	if _, ok := l.addresses[ServerRequestBodyAddr]; ok {
		dyngo.On(op, shared.MakeWAFRunListener(&op.SecurityEventsHolder, wafCtx, l.limiter, func(args types.SDKBodyOperationArgs) waf.RunAddressData {
			return waf.RunAddressData{Persistent: map[string]any{ServerRequestBodyAddr: args.Body}}
		}))
	}

	dyngo.OnFinish(op, func(op *types.Operation, res types.HandlerOperationRes) {
		defer wafCtx.Close()

		values = make(map[string]any, 3)
		if l.canExtractSchemas() {
			// This address will be passed as persistent. The WAF will keep it in store and trigger schema extraction
			// for each run.
			values["waf.context.processor"] = map[string]any{"extract-schema": true}
		}

		if _, ok := l.addresses[ServerResponseStatusAddr]; ok {
			// serverResponseStatusAddr is a string address, so we must format the status code...
			values[ServerResponseStatusAddr] = fmt.Sprintf("%d", res.Status)
		}

		if _, ok := l.addresses[ServerResponseHeadersNoCookiesAddr]; ok && res.Headers != nil {
			values[ServerResponseHeadersNoCookiesAddr] = res.Headers
		}

		// Run the WAF, ignoring the returned actions - if any - since blocking after the request handler's
		// response is not supported at the moment.
		wafResult := shared.RunWAF(wafCtx, waf.RunAddressData{Persistent: values})

		// Add WAF metrics.
		shared.AddWAFMonitoringTags(op, l.wafDiags.Version, wafCtx.Stats().Metrics())

		// Add the following metrics once per instantiation of a WAF handle
		l.once.Do(func() {
			shared.AddRulesMonitoringTags(op, &l.wafDiags)
			op.SetTag(ext.ManualKeep, samplernames.AppSec)
		})

		// Log the attacks if any
		if wafResult.HasEvents() {
			log.Debug("appsec: attack detected by the waf")
			shared.AddSecurityEvents(&op.SecurityEventsHolder, l.limiter, wafResult.Events)
		}
		for tag, value := range wafResult.Derivatives {
			op.AddSerializableTag(tag, value)
		}
	})
}

// canExtractSchemas checks that API Security is enabled and that sampling rate
// allows extracting schemas
func (l *wafEventListener) canExtractSchemas() bool {
	return l.config.APISec.Enabled && l.config.APISec.SampleRate >= rand.Float64()
}
