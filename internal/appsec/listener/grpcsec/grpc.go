// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package grpcsec

import (
	"sync"

	"go.uber.org/atomic"

	"github.com/DataDog/appsec-internal-go/limiter"
	waf "github.com/DataDog/go-libddwaf/v3"

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
	GRPCServerMethodAddr          = "grpc.server.method"
	GRPCServerRequestMessageAddr  = "grpc.server.request.message"
	GRPCServerRequestMetadataAddr = "grpc.server.request.metadata"
	HTTPClientIPAddr              = httpsec.HTTPClientIPAddr
	UserIDAddr                    = httpsec.UserIDAddr
)

// List of gRPC rule addresses currently supported by the WAF
var supportedAddresses = listener.AddressSet{
	GRPCServerMethodAddr:          {},
	GRPCServerRequestMessageAddr:  {},
	GRPCServerRequestMetadataAddr: {},
	HTTPClientIPAddr:              {},
	UserIDAddr:                    {},
}

// Install registers the gRPC WAF Event Listener on the given root operation.
func Install(wafHandle *waf.Handle, cfg *config.Config, lim limiter.Limiter, root dyngo.Operation) {
	if listener := newWafEventListener(wafHandle, cfg, lim); listener != nil {
		log.Debug("appsec: registering the gRPC WAF Event Listener")
		dyngo.On(root, listener.onEvent)
	}
}

type wafEventListener struct {
	wafHandle *waf.Handle
	config    *config.Config
	addresses map[string]struct{}
	limiter   limiter.Limiter
	wafDiags  waf.Diagnostics
	once      sync.Once
}

func newWafEventListener(wafHandle *waf.Handle, cfg *config.Config, limiter limiter.Limiter) *wafEventListener {
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
		addresses: addresses,
		limiter:   limiter,
		wafDiags:  wafHandle.Diagnostics(),
	}
}

// NewWAFEventListener returns the WAF event listener to register in order to enable it, listening to gRPC handler
// events.
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

	wafCtx, err := l.wafHandle.NewContextWithBudget(l.config.WAFTimeout)
	if err != nil {
		log.Debug("appsec: could not create budgeted WAF context: %v", err)
	}
	// Early return in the following cases:
	// - wafCtx is nil, meaning it was concurrently released
	// - err is not nil, meaning context creation failed
	if wafCtx == nil || err != nil {
		return
	}

	// Listen to the UserID address if the WAF rules are using it
	if l.isSecAddressListened(UserIDAddr) {
		// UserIDOperation happens when appsec.SetUser() is called. We run the WAF and apply actions to
		// see if the associated user should be blocked. Since we don't control the execution flow in this case
		// (SetUser is SDK), we delegate the responsibility of interrupting the handler to the user.
		dyngo.On(op, func(userIDOp *sharedsec.UserIDOperation, args sharedsec.UserIDOperationArgs) {
			values := map[string]any{
				UserIDAddr: args.UserID,
			}
			wafResult := shared.RunWAF(wafCtx, waf.RunAddressData{Persistent: values})
			if wafResult.HasActions() || wafResult.HasEvents() {
				for aType, params := range wafResult.Actions {
					for _, action := range shared.ActionsFromEntry(aType, params) {
						if grpcAction, ok := action.(*sharedsec.GRPCAction); ok {
							code, err := grpcAction.GRPCWrapper(map[string][]string{})
							dyngo.EmitData(userIDOp, types.NewMonitoringError(err.Error(), code))
						}
					}
				}
				shared.AddSecurityEvents(op, l.limiter, wafResult.Events)
				log.Debug("appsec: WAF detected an authenticated user attack: %s", args.UserID)
			}
		})
	}

	values := make(map[string]any, 2) // 2 because the method and client ip addresses are commonly present in the rules
	if l.isSecAddressListened(GRPCServerMethodAddr) {
		// Note that this address is passed asap for the passlist, which are created per grpc method
		values[GRPCServerMethodAddr] = handlerArgs.Method
	}
	if l.isSecAddressListened(HTTPClientIPAddr) && handlerArgs.ClientIP.IsValid() {
		values[HTTPClientIPAddr] = handlerArgs.ClientIP.String()
	}

	wafResult := shared.RunWAF(wafCtx, waf.RunAddressData{Persistent: values})
	if wafResult.HasActions() || wafResult.HasEvents() {
		interrupt := shared.ProcessActions(op, wafResult.Actions, nil)
		shared.AddSecurityEvents(op, l.limiter, wafResult.Events)
		log.Debug("appsec: WAF detected an attack before executing the request")
		if interrupt {
			wafCtx.Close()
			return
		}
	}

	// When the gRPC handler receives a message
	dyngo.OnFinish(op, func(_ types.ReceiveOperation, res types.ReceiveOperationRes) {
		if nbEvents.Load() == maxWAFEventsPerRequest {
			logOnce.Do(func() {
				log.Debug("appsec: ignoring the rpc message due to the maximum number of security events per grpc call reached")
			})
			return
		}

		// Run the WAF on the rule addresses available and listened to by the sec rules
		var values waf.RunAddressData
		// Add the gRPC message to the values if the WAF rules are using it.
		// Note that it is an ephemeral address as they can happen more than once per RPC.
		if l.isSecAddressListened(GRPCServerRequestMessageAddr) {
			values.Ephemeral = map[string]any{GRPCServerRequestMessageAddr: res.Message}
		}

		// Add the metadata to the values if the WAF rules are using it.
		if l.isSecAddressListened(GRPCServerRequestMetadataAddr) {
			if md := handlerArgs.Metadata; len(md) > 0 {
				values.Persistent = map[string]any{GRPCServerRequestMetadataAddr: md}
			}
		}

		// Run the WAF, ignoring the returned actions - if any - since blocking after the request handler's
		// response is not supported at the moment.
		wafResult := shared.RunWAF(wafCtx, values)

		if wafResult.HasEvents() {
			log.Debug("appsec: attack detected by the grpc waf")
			nbEvents.Inc()
			mu.Lock()
			defer mu.Unlock()
			events = append(events, wafResult.Events...)
		}
	})

	// When the gRPC handler finishes
	dyngo.OnFinish(op, func(op *types.HandlerOperation, _ types.HandlerOperationRes) {
		defer wafCtx.Close()
		shared.AddWAFMonitoringTags(op, l.wafDiags.Version, wafCtx.Stats().Metrics())

		// Log the following metrics once per instantiation of a WAF handle
		l.once.Do(func() {
			shared.AddRulesMonitoringTags(op, &l.wafDiags)
			op.SetTag(ext.ManualKeep, samplernames.AppSec)
		})

		shared.AddSecurityEvents(op, l.limiter, events)
	})
}

func (l *wafEventListener) isSecAddressListened(addr string) bool {
	_, listened := l.addresses[addr]
	return listened
}
