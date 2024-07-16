// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package graphqlsec

import (
	"sync"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/config"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/graphqlsec/types"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/listener"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/listener/httpsec"
	shared "gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/listener/sharedsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/trace"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/samplernames"

	"github.com/DataDog/appsec-internal-go/limiter"
	waf "github.com/DataDog/go-libddwaf/v3"
)

// GraphQL rule addresses currently supported by the WAF
const (
	graphQLServerResolverAddr = "graphql.server.resolver"
)

// List of GraphQL rule addresses currently supported by the WAF
var supportedAddresses = listener.AddressSet{
	graphQLServerResolverAddr:  {},
	httpsec.ServerIoNetURLAddr: {},
}

// Install registers the GraphQL WAF Event Listener on the given root operation.
func Install(wafHandle *waf.Handle, cfg *config.Config, lim limiter.Limiter, root dyngo.Operation) {
	if listener := newWafEventListener(wafHandle, cfg, lim); listener != nil {
		log.Debug("appsec: registering the GraphQL WAF Event Listener")
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

func newWafEventListener(wafHandle *waf.Handle, cfg *config.Config, limiter limiter.Limiter) *wafEventListener {
	if wafHandle == nil {
		log.Debug("appsec: no WAF Handle available, the GraphQL WAF Event Listener will not be registered")
		return nil
	}

	addresses := listener.FilterAddressSet(supportedAddresses, wafHandle)
	if len(addresses) == 0 {
		log.Debug("appsec: no supported GraphQL address is used by currently loaded WAF rules, the GraphQL WAF Event Listener will not be registered")
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

// NewWAFEventListener returns the WAF event listener to register in order
// to enable it.
func (l *wafEventListener) onEvent(request *types.RequestOperation, _ types.RequestOperationArgs) {
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

	if _, ok := l.addresses[httpsec.ServerIoNetURLAddr]; ok {
		httpsec.RegisterRoundTripperListener(request, &request.SecurityEventsHolder, wafCtx, l.limiter)
	}

	// Add span tags notifying this trace is AppSec-enabled
	trace.SetAppSecEnabledTags(request)
	l.once.Do(func() {
		shared.AddRulesMonitoringTags(request, &l.wafDiags)
		request.SetTag(ext.ManualKeep, samplernames.AppSec)
	})

	dyngo.On(request, func(query *types.ExecutionOperation, args types.ExecutionOperationArgs) {
		dyngo.On(query, func(field *types.ResolveOperation, args types.ResolveOperationArgs) {
			if _, found := l.addresses[graphQLServerResolverAddr]; found {
				wafResult := shared.RunWAF(
					wafCtx,
					waf.RunAddressData{
						Ephemeral: map[string]any{
							graphQLServerResolverAddr: map[string]any{args.FieldName: args.Arguments},
						},
					},
				)
				shared.AddSecurityEvents(&field.SecurityEventsHolder, l.limiter, wafResult.Events)
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

		shared.AddWAFMonitoringTags(request, l.wafDiags.Version, wafCtx.Stats().Metrics())
		trace.SetEventSpanTags(request, request.Events())
	})
}
