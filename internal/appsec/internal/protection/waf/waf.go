// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package waf

import (
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	httpinstr "gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/instrumentation/http"
	waftypes "gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/internal/protection/waf/types"
	appsectypes "gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/types"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"

	"github.com/sqreen/go-libsqreen/waf"
	"github.com/sqreen/go-libsqreen/waf/types"
)

type EventManager interface {
	SendEvent(event *appsectypes.SecurityEvent)
}

// List of rule addresses currently supported by the WAF
const (
	serverRequestRawURIAddr  = "server.request.uri.raw"
	serverRequestHeadersAddr = "server.request.headers.no_cookies"
)

// NewOperationEventListener returns the WAF event listener to register in order to enable it.
func NewOperationEventListener(appsec EventManager) dyngo.EventListener {
	wafRule, err := waf.NewRule(staticWAFRule)
	if err != nil {
		log.Error("appsec: waf error: %v", err)
		return nil
	}

	return httpinstr.OnHandlerOperationStartListener(func(op *dyngo.Operation, args httpinstr.HandlerOperationArgs) {
		// For this handler operation lifetime, create a WAF context and the list of detected attacks
		var (
			// TODO(julio): make the attack slice thread-safe once we listen for sub-operations
			attacks []waftypes.RawAttackMetadata
			wafCtx  = waf.NewAdditiveContext(wafRule)
		)

		httpinstr.OnHandlerOperationFinish(op, func(op *dyngo.Operation, res httpinstr.HandlerOperationRes) {
			// Release the WAF context
			wafCtx.Close()
			// Log the attacks if any
			if len(attacks) > 0 {
				// Create the base security event out of the slide of attacks
				event := appsectypes.NewSecurityEvent(attacks, httpinstr.MakeHTTPOperationContext(args, res))
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
		})

		// Run the WAF on the rule addresses available in the request args
		// TODO(julio): dynamically get the required addresses from the WAF rule
		headers := args.Headers.Clone()
		headers.Del("Cookie")
		values := map[string]interface{}{
			serverRequestRawURIAddr:  args.RequestURI,
			serverRequestHeadersAddr: headers,
		}
		runWAF(wafCtx, values, &attacks)
	})
}

func runWAF(wafCtx types.Rule, values types.DataSet, attacks *[]waftypes.RawAttackMetadata) {
	action, md, err := wafCtx.Run(values, 1*time.Millisecond)
	if err != nil {
		log.Error("appsec: waf error: %v", err)
		return
	}
	if action == types.NoAction {
		return
	}
	*attacks = append(*attacks, waftypes.RawAttackMetadata{Time: time.Now(), Block: action == types.BlockAction, Metadata: md})
}
