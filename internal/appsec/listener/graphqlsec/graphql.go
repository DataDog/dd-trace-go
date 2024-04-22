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
	"gopkg.in/DataDog/dd-trace-go.v1/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/dyngo/event/graphqlevent"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/config"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/sharedsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/listener"
	shared "gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/listener/sharedsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/trace"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/samplernames"
)

type productConfig struct {
	wafHandle *waf.Handle
	config    *config.Config
	limiter   limiter.Limiter
	mu        sync.RWMutex
}

var Product = &productConfig{}

func (*productConfig) Name() string {
	return "appsec.GraphQL"
}

func (c *productConfig) Configure(wafHandle *waf.Handle, _ sharedsec.Actions, cfg *config.Config, lim limiter.Limiter) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.wafHandle = wafHandle
	c.config = cfg
	c.limiter = lim
}

func (c *productConfig) Start(op dyngo.Operation) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if listener := newWafEventListener(c.wafHandle, c.config, c.limiter); listener != nil {
		dyngo.On(op, listener.onEvent)
	}
}

// GraphQL rule addresses currently supported by the WAF
const (
	graphQLServerResolverAddr = "graphql.server.resolver"
)

// List of GraphQL rule addresses currently supported by the WAF
var supportedAddresses = listener.AddressSet{
	graphQLServerResolverAddr: {},
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
func (l *wafEventListener) onEvent(request *graphqlevent.RequestOperation, _ graphqlevent.RequestOperationArgs) {
	wafCtx := waf.NewContextWithBudget(l.wafHandle, l.config.WAFTimeout)
	if wafCtx == nil {
		return
	}

	// Add span tags notifying this trace is AppSec-enabled
	trace.SetAppSecEnabledTags(request)
	l.once.Do(func() {
		shared.AddRulesMonitoringTags(request, &l.wafDiags)
		request.SetTag(ext.ManualKeep, samplernames.AppSec)
	})

	dyngo.On(request, func(query *graphqlevent.ExecutionOperation, args graphqlevent.ExecutionOperationArgs) {
		dyngo.On(query, func(field *graphqlevent.ResolveOperation, args graphqlevent.ResolveOperationArgs) {
			if _, found := l.addresses[graphQLServerResolverAddr]; found {
				wafResult := shared.RunWAF(
					wafCtx,
					waf.RunAddressData{
						Ephemeral: map[string]any{
							graphQLServerResolverAddr: map[string]any{args.FieldName: args.Arguments},
						},
					},
					l.config.WAFTimeout,
				)
				shared.AddSecurityEvents(field, l.limiter, wafResult.Events)
			}

			dyngo.OnFinish(field, func(field *graphqlevent.ResolveOperation, res graphqlevent.ResolveOperationRes) {
				trace.SetEventSpanTags(field, field.Events())
			})
		})

		dyngo.OnFinish(query, func(query *graphqlevent.ExecutionOperation, res graphqlevent.ExecutionOperationRes) {
			trace.SetEventSpanTags(query, query.Events())
		})
	})

	dyngo.OnFinish(request, func(request *graphqlevent.RequestOperation, res graphqlevent.RequestOperationRes) {
		defer wafCtx.Close()

		shared.AddWAFMonitoringTags(request, l.wafDiags.Version, wafCtx.Stats().Metrics())
		trace.SetEventSpanTags(request, request.Events())
	})
}
