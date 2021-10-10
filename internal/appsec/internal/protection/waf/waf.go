// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package waf

import (
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/internal/protection/waf/internal/bindings"
	waftypes "gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/internal/protection/waf/types"
	appsectypes "gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/types"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/dyngo/instrumentation"
	httpinstr "gopkg.in/DataDog/dd-trace-go.v1/internal/dyngo/instrumentation/http"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

type EventManager interface {
	SendEvent(event *appsectypes.SecurityEvent)
}

// List of rule addresses currently supported by the WAF
const (
	serverRequestRawURIAddr           = "server.request.uri.raw"
	serverRequestHeadersNoCookiesAddr = "server.request.headers.no_cookies"
)

// Register the WAF event listener.
func Register(rules []byte, appsec EventManager) (instrumentation.UnregisterFunc, error) {
	if version, err := bindings.Health(); err != nil {
		return nil, err
	} else {
		log.Debug("appsec: registering waf v%s instrumentation", version.String())
	}
	if rules == nil {
		rules = []byte(staticRecommendedRule)
	}
	waf, err := bindings.NewWAF(rules)
	if err != nil {
		return nil, err
	}
	unregister := dyngo.Register(newWAFEventListener(waf, appsec))
	return func() {
		defer waf.Close()
		unregister()
	}, nil
}

// newWAFEventListener returns the WAF event listener to register in order to enable it.
func newWAFEventListener(waf *bindings.WAF, appsec EventManager) instrumentation.EventListener {
	return httpinstr.OnHandlerOperationStart(func(op instrumentation.Operation, args httpinstr.HandlerOperationArgs) {
		wafCtx := bindings.NewWAFContext(waf)
		if wafCtx == nil {
			// The WAF event listener got concurrently released
			return
		}

		// For this handler operation lifetime, create a WAF context and the list of detected attacks
		var (
			// TODO(julio): make the attack slice thread-safe once we listen for sub-operations
			attacks []waftypes.RawAttackMetadata
		)

		op.On(httpinstr.OnHandlerOperationFinish(func(op instrumentation.Operation, res httpinstr.HandlerOperationRes) {
			// Release the WAF context
			wafCtx.Close()
			// Log the attacks if any
			if len(attacks) > 0 {
				// Create the base security event out of the slide of attacks
				event := appsectypes.NewSecurityEvent(attacks, makeHTTPOperationContext(args, res))
				// Check if a span exists
				if span := args.Span; span != nil {
					// Add the span context to the security event if any
					spanCtx := span.Context()

					event.AddContext(appsectypes.SpanContext{
						TraceID: spanCtx.TraceID(),
						SpanID:  spanCtx.SpanID(),
					})
					// Keep this span due to the security event
					span.SetTag(ext.SamplingPriority, ext.ManualKeep)
				}
				appsec.SendEvent(event)
			}
		}))

		// Run the WAF on the rule addresses available in the request args
		// TODO(julio): dynamically get the required addresses from the WAF rule
		values := map[string]interface{}{
			serverRequestRawURIAddr:           args.RequestURI,
			serverRequestHeadersNoCookiesAddr: args.Headers,
		}
		runWAF(wafCtx, values, &attacks)
	})
}

func runWAF(wafCtx *bindings.WAFContext, values map[string]interface{}, attacks *[]waftypes.RawAttackMetadata) {
	action, md, err := wafCtx.Run(values, 1*time.Millisecond)
	if err != nil {
		log.Error("appsec: waf error: %v", err)
		return
	}
	if action == bindings.NoAction {
		return
	}
	*attacks = append(*attacks, waftypes.RawAttackMetadata{Time: time.Now(), Block: action == bindings.BlockAction, Metadata: md})
}

// makeHTTPOperationContext creates an HTTP operation context from HTTP operation arguments and results.
// This context can be added to a security event.
func makeHTTPOperationContext(args httpinstr.HandlerOperationArgs, res httpinstr.HandlerOperationRes) appsectypes.HTTPOperationContext {
	return appsectypes.HTTPOperationContext{
		Request: appsectypes.HTTPRequestContext{
			Method:     args.Method,
			Host:       args.Host,
			IsTLS:      args.IsTLS,
			RequestURI: args.RequestURI,
			RemoteAddr: args.RemoteAddr,
		},
		Response: appsectypes.HTTPResponseContext{
			Status: res.Status,
		},
	}
}
