// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package graphqlsec

import (
	"sync"

	"github.com/DataDog/appsec-internal-go/limiter"
	waf "github.com/DataDog/go-libddwaf/v2"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/config"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/graphqlsec/types"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/sharedsec"
	listener "gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/listener/sharedsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/trace"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/samplernames"
)

// GraphQL rule addresses currently supported by the WAF
const (
	graphQLServerResolverAddr = "graphql.server.resolver"
)

// List of GraphQL rule addresses currently supported by the WAF
var supportedAddresses = map[string]struct{}{
	graphQLServerResolverAddr: {},
}

func Install(wafHandle *waf.Handle, _ sharedsec.Actions, cfg *config.Config, lim limiter.Limiter, root dyngo.Operation) {
	if listener := newWafEventListener(wafHandle, cfg, lim); listener != nil {
		log.Debug("[appsec] registering the GraphQL WAF Event Listener")
		dyngo.On(root, listener.onEvent)
	}
}

type wafEventListener struct {
	limiter   limiter.Limiter
	wafHandle *waf.Handle
	config    *config.Config
	addresses map[string]struct{}
	wafDiags  waf.Diagnostics
	once      sync.Once
}

func newWafEventListener(wafHandle *waf.Handle, cfg *config.Config, limiter limiter.Limiter) *wafEventListener {
	if wafHandle == nil {
		log.Debug("[appsec] no WAF Handle available, the GraphQL WAF Event Listener will not be registered")
		return nil
	}

	addresses := make(map[string]struct{}, len(supportedAddresses))
	wafDiags := wafHandle.Diagnostics()
	for _, addr := range wafDiags.Rules.Addresses.Required {
		if _, found := supportedAddresses[addr]; found {
			addresses[addr] = struct{}{}
		}
	}

	if len(addresses) == 0 {
		log.Debug("[appsec] no supported GraphQL address is used by currently loaded WAF rules, the GraphQL WAF Event Listener will not be registered")
		return nil
	}

	return &wafEventListener{
		wafHandle: wafHandle,
		wafDiags:  wafDiags,
		config:    cfg,
		limiter:   limiter,
		addresses: addresses,
	}
}

// NewWAFEventListener returns the WAF event listener to register in order
// to enable it.
func (l *wafEventListener) onEvent(request *types.RequestOperation, _ types.RequestOperationArgs) {
	wafCtx := waf.NewContext(l.wafHandle)
	if wafCtx == nil {
		return
	}

	// Add span tags notifying this trace is AppSec-enabled
	trace.SetAppSecEnabledTags(request)
	l.once.Do(func() {
		listener.AddRulesMonitoringTags(request, &l.wafDiags)
		request.SetTag(ext.ManualKeep, samplernames.AppSec)
	})

	dyngo.On(request, func(query *types.ExecutionOperation, args types.ExecutionOperationArgs) {
		dyngo.On(query, func(field *types.ResolveOperation, args types.ResolveOperationArgs) {
			if _, found := l.addresses[graphQLServerResolverAddr]; found {
				wafResult := listener.RunWAF(
					wafCtx,
					waf.RunAddressData{
						Ephemeral: map[string]any{
							graphQLServerResolverAddr: map[string]any{args.FieldName: args.Arguments},
						},
					},
					l.config.WAFTimeout,
				)
				listener.AddSecurityEvents(field, l.limiter, wafResult.Events)
			}

			dyngo.OnFinish(field, func(field *types.ResolveOperation, res types.ResolveOperationRes) {
				trace.SetEventSpanTags(field, field.Events())
			})
		})

		dyngo.OnFinish(query, func(query *types.ExecutionOperation, res types.ExecutionOperationRes) {
			trace.SetEventSpanTags(query, query.Events())
		})
	})

	dyngo.OnFinish(request, func(request *types.RequestOperation, res types.RequestOperationRes) {
		defer wafCtx.Close()

		overall, internal := wafCtx.TotalRuntime()
		nbTimeouts := wafCtx.TotalTimeouts()
		listener.AddWAFMonitoringTags(request, l.wafDiags.Version, overall, internal, nbTimeouts)

		trace.SetEventSpanTags(request, request.Events())
	})
}
