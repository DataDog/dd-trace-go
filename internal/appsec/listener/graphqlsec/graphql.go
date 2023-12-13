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
	graphQLServerResolverAddr = "graphql.server.resolver"
)

// List of GraphQL rule addresses currently supported by the WAF
var supportedAddresses = map[string]struct{}{
	graphQLServerResolverAddr: {},
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
	wafDiags := handle.Diagnostics()

	return graphqlsec.OnRequestOperationStart(func(request *graphqlsec.RequestOperation, args graphqlsec.RequestOperationArgs) {
		wafCtx := waf.NewContext(handle)
		if wafCtx == nil {
			return
		}

		// Add span tags notifying this trace is AppSec-enabled
		trace.SetAppSecEnabledTags(request)
		rulesMonitoringOnce.Do(func() {
			listener.AddRulesMonitoringTags(request, &wafDiags)
			request.SetTag(ext.ManualKeep, samplernames.AppSec)
		})

		request.On(graphqlsec.OnExecutionOperationStart(func(query *graphqlsec.ExecutionOperation, args graphqlsec.ExecutionOperationArgs) {
			query.On(graphqlsec.OnResolveOperationStart(func(field *graphqlsec.ResolveOperation, args graphqlsec.ResolveOperationArgs) {
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

				field.On(graphqlsec.OnResolveOperationFinish(func(field *graphqlsec.ResolveOperation, res graphqlsec.ResolveOperationRes) {
					trace.SetEventSpanTags(field, field.Events())
				}))
			}))

			query.On(graphqlsec.OnExecutionOperationFinish(func(query *graphqlsec.ExecutionOperation, res graphqlsec.ExecutionOperationRes) {
				trace.SetEventSpanTags(query, query.Events())
			}))
		}))

		request.On(graphqlsec.OnRequestOperationFinish(func(request *graphqlsec.RequestOperation, res graphqlsec.RequestOperationRes) {
			defer wafCtx.Close()

			overall, internal := wafCtx.TotalRuntime()
			nbTimeouts := wafCtx.TotalTimeouts()
			listener.AddWAFMonitoringTags(request, wafDiags.Version, overall, internal, nbTimeouts)

			trace.SetEventSpanTags(request, request.Events())
		}))
	})
}
