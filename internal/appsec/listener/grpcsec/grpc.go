// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package grpcsec

import (
	"sync"

	"go.uber.org/atomic"

	"github.com/DataDog/appsec-internal-go/limiter"
	waf "github.com/DataDog/go-libddwaf/v2"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/config"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/grpcsec/types"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/sharedsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/listener"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/listener/httpsec"
	shared "gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/listener/sharedsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/samplernames"
)

// gRPC rule addresses currently supported by the WAF
const (
	GRPCServerRequestMessage  = "grpc.server.request.message"
	GRPCServerRequestMetadata = "grpc.server.request.metadata"
	HTTPClientIPAddr          = httpsec.HTTPClientIPAddr
	UserIDAddr                = httpsec.UserIDAddr
)

// List of gRPC rule addresses currently supported by the WAF
var supportedAddresses = listener.AddressSet{
	GRPCServerRequestMessage:  {},
	GRPCServerRequestMetadata: {},
	HTTPClientIPAddr:          {},
	UserIDAddr:                {},
}

// Install registers the gRPC WAF Event Listener on the given root operation.
func Install(wafHandle *waf.Handle, actions sharedsec.Actions, cfg *config.Config, lim limiter.Limiter, root dyngo.Operation) {
	if listener := newWafEventListener(wafHandle, actions, cfg, lim); listener != nil {
		log.Debug("appsec: registering the gRPC WAF Event Listener")
		dyngo.On(root, listener.onEvent)
	}
}

type wafEventListener struct {
	wafHandle *waf.Handle
	config    *config.Config
	actions   sharedsec.Actions
	addresses map[string]struct{}
	limiter   limiter.Limiter
	wafDiags  waf.Diagnostics
	once      sync.Once
}

func newWafEventListener(wafHandle *waf.Handle, actions sharedsec.Actions, cfg *config.Config, limiter limiter.Limiter) *wafEventListener {
	if wafHandle == nil {
		log.Debug("appsec: no WAF Handle available, the gRPC WAF Event Listener will not be registered")
		return nil
	}

	addresses := listener.FilterAddressSet(supportedAddresses, wafHandle)
	if len(addresses) == 0 {
		log.Debug("appsec: no supported gRPC address is used by currently loaded WAF rules, the gRPC WAF Event Listener will not be registered")
		return nil
	}

	return &wafEventListener{
		wafHandle: wafHandle,
		config:    cfg,
		actions:   actions,
		addresses: addresses,
		limiter:   limiter,
		wafDiags:  wafHandle.Diagnostics(),
	}
}

// NewWAFEventListener returns the WAF event listener to register in order
// to enable it.
func (l *wafEventListener) onEvent(op *types.HandlerOperation, handlerArgs types.HandlerOperationArgs) {
	// Limit the maximum number of security events, as a streaming RPC could
	// receive unlimited number of messages where we could find security events
	const maxWAFEventsPerRequest = 10
	var (
		nbEvents atomic.Uint32
		logOnce  sync.Once // per request

		events []any
		mu     sync.Mutex // events mutex
	)

	wafCtx := waf.NewContext(l.wafHandle)
	if wafCtx == nil {
		// The WAF event listener got concurrently released
		return
	}

	// OnUserIDOperationStart happens when appsec.SetUser() is called. We run the WAF and apply actions to
	// see if the associated user should be blocked. Since we don't control the execution flow in this case
	// (SetUser is SDK), we delegate the responsibility of interrupting the handler to the user.
	dyngo.On(op, func(userIDOp *sharedsec.UserIDOperation, args sharedsec.UserIDOperationArgs) {
		values := make(map[string]any, 1)
		for addr := range l.addresses {
			if addr == UserIDAddr {
				values[UserIDAddr] = args.UserID
			}
		}
		wafResult := shared.RunWAF(wafCtx, waf.RunAddressData{Persistent: values}, l.config.WAFTimeout)
		if wafResult.HasActions() || wafResult.HasEvents() {
			for _, id := range wafResult.Actions {
				if a, ok := l.actions[id]; ok && a.Blocking() {
					code, err := a.GRPC()(map[string][]string{})
					dyngo.EmitData(userIDOp, types.NewMonitoringError(err.Error(), code))
				}
			}
			shared.AddSecurityEvents(op, l.limiter, wafResult.Events)
			log.Debug("appsec: WAF detected an authenticated user attack: %s", args.UserID)
		}
	})

	// The same address is used for gRPC and http when it comes to client ip
	values := make(map[string]any, 1)
	for addr := range l.addresses {
		if addr == HTTPClientIPAddr && handlerArgs.ClientIP.IsValid() {
			values[HTTPClientIPAddr] = handlerArgs.ClientIP.String()
		}
	}

	wafResult := shared.RunWAF(wafCtx, waf.RunAddressData{Persistent: values}, l.config.WAFTimeout)
	if wafResult.HasActions() || wafResult.HasEvents() {
		interrupt := shared.ProcessActions(op, l.actions, wafResult.Actions)
		shared.AddSecurityEvents(op, l.limiter, wafResult.Events)
		log.Debug("appsec: WAF detected an attack before executing the request")
		if interrupt {
			wafCtx.Close()
			return
		}
	}

	dyngo.OnFinish(op, func(_ types.ReceiveOperation, res types.ReceiveOperationRes) {
		if nbEvents.Load() == maxWAFEventsPerRequest {
			logOnce.Do(func() {
				log.Debug("appsec: ignoring the rpc message due to the maximum number of security events per grpc call reached")
			})
			return
		}

		// Run the WAF on the rule addresses available in the args
		// Note that we don't check if the address is present in the rules
		// as we only support one at the moment, so this callback cannot be
		// set when the address is not present.
		values := waf.RunAddressData{
			Ephemeral: map[string]any{GRPCServerRequestMessage: res.Message},
		}
		if md := handlerArgs.Metadata; len(md) > 0 {
			values.Persistent = map[string]any{GRPCServerRequestMetadata: md}
		}
		// Run the WAF, ignoring the returned actions - if any - since blocking after the request handler's
		// response is not supported at the moment.
		wafResult := shared.RunWAF(wafCtx, values, l.config.WAFTimeout)

		if wafResult.HasEvents() {
			log.Debug("appsec: attack detected by the grpc waf")
			nbEvents.Inc()
			mu.Lock()
			defer mu.Unlock()
			events = append(events, wafResult.Events...)
		}
	})

	dyngo.OnFinish(op, func(op *types.HandlerOperation, _ types.HandlerOperationRes) {
		defer wafCtx.Close()
		overallRuntimeNs, internalRuntimeNs := wafCtx.TotalRuntime()
		shared.AddWAFMonitoringTags(op, l.wafDiags.Version, overallRuntimeNs, internalRuntimeNs, wafCtx.TotalTimeouts())

		// Log the following metrics once per instantiation of a WAF handle
		l.once.Do(func() {
			shared.AddRulesMonitoringTags(op, &l.wafDiags)
			op.SetTag(ext.ManualKeep, samplernames.AppSec)
		})

		shared.AddSecurityEvents(op, l.limiter, events)
	})
}
