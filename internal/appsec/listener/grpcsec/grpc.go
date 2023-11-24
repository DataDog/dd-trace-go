// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package grpcsec

import (
	"sync"
	"time"

	"go.uber.org/atomic"

	"github.com/DataDog/appsec-internal-go/limiter"
	waf "github.com/DataDog/go-libddwaf/v2"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/grpcsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/sharedsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/listener/httpsec"
	listener "gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/listener/sharedsec"
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
var supportedpAddresses = map[string]struct{}{
	GRPCServerRequestMessage:  {},
	GRPCServerRequestMetadata: {},
	HTTPClientIPAddr:          {},
	UserIDAddr:                {},
}

func SupportsAddress(addr string) bool {
	_, ok := supportedpAddresses[addr]
	return ok
}

// NewWAFEventListener returns the WAF event listener to register in order
// to enable it.
func NewWAFEventListener(handle *waf.Handle, actions sharedsec.Actions, addresses map[string]struct{}, timeout time.Duration, limiter limiter.Limiter) dyngo.EventListener {
	var monitorRulesOnce sync.Once // per instantiation
	wafDiags := handle.Diagnostics()

	return grpcsec.OnHandlerOperationStart(func(op *grpcsec.HandlerOperation, handlerArgs grpcsec.HandlerOperationArgs) {
		// Limit the maximum number of security events, as a streaming RPC could
		// receive unlimited number of messages where we could find security events
		const maxWAFEventsPerRequest = 10
		var (
			nbEvents atomic.Uint32
			logOnce  sync.Once // per request

			events []any
			mu     sync.Mutex // events mutex
		)

		wafCtx := waf.NewContext(handle)
		if wafCtx == nil {
			// The WAF event listener got concurrently released
			return
		}

		// OnUserIDOperationStart happens when appsec.SetUser() is called. We run the WAF and apply actions to
		// see if the associated user should be blocked. Since we don't control the execution flow in this case
		// (SetUser is SDK), we delegate the responsibility of interrupting the handler to the user.
		op.On(sharedsec.OnUserIDOperationStart(func(userIDOp *sharedsec.UserIDOperation, args sharedsec.UserIDOperationArgs) {
			values := map[string]any{}
			for addr := range addresses {
				if addr == UserIDAddr {
					values[UserIDAddr] = args.UserID
				}
			}
			wafResult := listener.RunWAF(wafCtx, waf.RunAddressData{Persistent: values}, timeout)
			if wafResult.HasActions() || wafResult.HasEvents() {
				for _, id := range wafResult.Actions {
					if a, ok := actions[id]; ok && a.Blocking() {
						code, err := a.GRPC()(map[string][]string{})
						userIDOp.EmitData(grpcsec.NewMonitoringError(err.Error(), code))
					}
				}
				listener.AddSecurityEvents(op, limiter, wafResult.Events)
				log.Debug("appsec: WAF detected an authenticated user attack: %s", args.UserID)
			}
		}))

		// The same address is used for gRPC and http when it comes to client ip
		values := map[string]any{}
		for addr := range addresses {
			if addr == HTTPClientIPAddr && handlerArgs.ClientIP.IsValid() {
				values[HTTPClientIPAddr] = handlerArgs.ClientIP.String()
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

		op.On(grpcsec.OnReceiveOperationFinish(func(_ grpcsec.ReceiveOperation, res grpcsec.ReceiveOperationRes) {
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
			wafResult := listener.RunWAF(wafCtx, values, timeout)

			if wafResult.HasEvents() {
				log.Debug("appsec: attack detected by the grpc waf")
				nbEvents.Inc()
				mu.Lock()
				defer mu.Unlock()
				events = append(events, wafResult.Events...)
			}
		}))

		op.On(grpcsec.OnHandlerOperationFinish(func(op *grpcsec.HandlerOperation, _ grpcsec.HandlerOperationRes) {
			defer wafCtx.Close()
			overallRuntimeNs, internalRuntimeNs := wafCtx.TotalRuntime()
			listener.AddWAFMonitoringTags(op, wafDiags.Version, overallRuntimeNs, internalRuntimeNs, wafCtx.TotalTimeouts())

			// Log the following metrics once per instantiation of a WAF handle
			monitorRulesOnce.Do(func() {
				listener.AddRulesMonitoringTags(op, &wafDiags)
				op.AddTag(ext.ManualKeep, samplernames.AppSec)
			})

			listener.AddSecurityEvents(op, limiter, events)
		}))
	})
}
