// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package graphqlsec

import (
	"sync"
	"time"

	"github.com/DataDog/appsec-internal-go/limiter"
	waf "github.com/DataDog/go-libddwaf/v2"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/graphqlsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/sharedsec"
	listener "gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/listener/sharedsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/trace"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/samplernames"
)

// GraphQL rule addresses currently supported by the WAF
const (
	graphQLServerAllResolversAddr = "graphql.server.all_resolvers"
	graphQLServerResolverAddr     = "graphql.server.resolver"
)

// List of GraphQL rule addresses currently supported by the WAF
var supportedAddresses = map[string]struct{}{
	graphQLServerAllResolversAddr: {},
	graphQLServerResolverAddr:     {},
}

func SupportedAddressCount() int {
	return len(supportedAddresses)
}

func SupportsAddress(addr string) bool {
	_, ok := supportedAddresses[addr]
	return ok
}

// NewWAFEventListener returns the WAF event listener to register in order
// to enable it.
func NewWAFEventListener(handle *waf.Handle, _ sharedsec.Actions, addresses map[string]struct{}, timeout time.Duration, limiter limiter.Limiter) dyngo.EventListener {
	var rulesMonitoringOnce sync.Once

	return graphqlsec.OnRequestStart(func(request *graphqlsec.Request, args graphqlsec.RequestArguments) {
		wafCtx := waf.NewContext(handle)
		if wafCtx == nil {
			return
		}
		wafDiags := handle.Diagnostics()

		// Add span tags notifying this trace is AppSec-enabled
		trace.SetAppSecEnabledTags(request)
		rulesMonitoringOnce.Do(func() {
			listener.AddRulesMonitoringTags(request, &wafDiags)
			request.SetTag(ext.ManualKeep, samplernames.AppSec)
		})

		request.On(graphqlsec.OnExecutionStart(func(query *graphqlsec.Execution, args graphqlsec.ExecutionArguments) {
			var (
				allResolvers   map[string][]map[string]any
				allResolversMu sync.Mutex
			)

			query.On(graphqlsec.OnFieldStart(func(field *graphqlsec.Field, args graphqlsec.FieldArguments) {
				if _, found := addresses[graphQLServerResolverAddr]; found {
					wafResult := listener.RunWAF(
						wafCtx,
						waf.RunAddressData{
							Ephemeral: map[string]any{
								graphQLServerResolverAddr: map[string]any{args.FieldName: args.Arguments},
							},
						},
						timeout,
					)
					listener.AddSecurityEvents(field, limiter, wafResult.Events)
				}

				if args.FieldName != "" {
					// Register in all resolvers
					allResolversMu.Lock()
					defer allResolversMu.Unlock()
					if allResolvers == nil {
						allResolvers = make(map[string][]map[string]any)
					}
					allResolvers[args.FieldName] = append(allResolvers[args.FieldName], args.Arguments)
				}

				field.On(graphqlsec.OnFieldFinish(func(field *graphqlsec.Field, res graphqlsec.FieldResult) {
					trace.SetEventSpanTags(field, field.Events())
				}))
			}))

			query.On(graphqlsec.OnExecutionFinish(func(query *graphqlsec.Execution, res graphqlsec.ExecutionResult) {
				if _, found := addresses[graphQLServerAllResolversAddr]; found && len(allResolvers) > 0 {
					// TODO: this is currently happening AFTER the resolvers have all run, which is... too late to block side-effects.
					wafResult := listener.RunWAF(wafCtx, waf.RunAddressData{Persistent: map[string]any{graphQLServerAllResolversAddr: allResolvers}}, timeout)
					listener.AddSecurityEvents(query, limiter, wafResult.Events)
				}
				trace.SetEventSpanTags(query, query.Events())
			}))
		}))

		request.On(graphqlsec.OnRequestFinish(func(request *graphqlsec.Request, res graphqlsec.RequestResult) {
			defer wafCtx.Close()

			overall, internal := wafCtx.TotalRuntime()
			nbTimeouts := wafCtx.TotalTimeouts()
			listener.AddWAFMonitoringTags(request, wafDiags.Version, overall, internal, nbTimeouts)

			trace.SetEventSpanTags(request, request.Events())
		}))
	})
}
