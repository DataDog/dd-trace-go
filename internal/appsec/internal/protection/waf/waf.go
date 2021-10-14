// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package waf

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	waftypes "gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/internal/protection/waf/types"
	appsectypes "gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/types"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/dyngo/instrumentation/httpinstr"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

// EventManager interface expected by the WAF to send its events.
type EventManager interface {
	SendEvent(event *appsectypes.SecurityEvent)
}

// Register the WAF event listener.
func Register(rules []byte, appsec EventManager) (unreg dyngo.UnregisterFunc, err error) {
	// Check the WAF is healthy
	if _, err := Health(); err != nil {
		return nil, err
	}

	// Instantiate the WAF
	if rules == nil {
		rules = []byte(staticRecommendedRule)
	}
	waf, err := newWAFHandle(rules)
	if err != nil {
		return nil, err
	}
	// Close the WAF in case of an error in what's following
	defer func() {
		if err != nil {
			waf.close()
		}
	}()

	// Check if there are addresses in the rule
	ruleAddresses := waf.addresses()
	if len(ruleAddresses) == 0 {
		return nil, errors.New("no addresses found in the rule")
	}
	// Check there are supported addresses in the rule
	addresses, notSupported := supportedAddresses(ruleAddresses)
	if len(addresses) == 0 {
		return nil, fmt.Errorf("the addresses present in the rule are not supported: %v", notSupported)
	} else if len(notSupported) > 0 {
		log.Debug("appsec: the addresses present in the rule are partially supported: supported=%v, not supported=%v", addresses, notSupported)
	}

	// Register the WAF event listener
	unregister := dyngo.Register(newWAFEventListener(waf, addresses, appsec))
	// Return an unregistration function that will also release the WAF instance.
	return func() {
		defer waf.close()
		unregister()
	}, nil
}

// newWAFEventListener returns the WAF event listener to register in order to enable it.
func newWAFEventListener(waf *wafHandle, addresses []string, appsec EventManager) dyngo.EventListener {
	return httpinstr.OnHandlerOperationStart(func(op dyngo.Operation, args httpinstr.HandlerOperationArgs) {
		wafCtx := newWAFContext(waf)
		if wafCtx == nil {
			// The WAF event listener got concurrently released
			return
		}

		// For this handler operation lifetime, create a WAF context and the
		// list of detected attacks
		// TODO(julio): make the attack slice thread-safe once we listen for
		//   sub-operations
		var attacks []waftypes.RawAttackMetadata

		op.On(httpinstr.OnHandlerOperationFinish(func(op dyngo.Operation, res httpinstr.HandlerOperationRes) {
			// Release the WAF context
			wafCtx.close()
			// Log the attacks if any
			if l := len(attacks); l > 0 {
				log.Debug("appsec: %d attacks detected", l)
				// Create the base security event out of the slide of attacks
				event := appsectypes.NewSecurityEvent(attacks, httpinstr.MakeHTTPContext(args, res))
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
		values := make(map[string]interface{}, len(addresses))
		for _, addr := range addresses {
			switch addr {
			case serverRequestRawURIAddr:
				values[serverRequestRawURIAddr] = args.RequestURI
			case serverRequestHeadersNoCookiesAddr:
				values[serverRequestHeadersNoCookiesAddr] = args.Headers
			case serverRequestCookiesAddr:
				values[serverRequestCookiesAddr] = args.Cookies
			case serverRequestQueryAddr:
				values[serverRequestQueryAddr] = args.Query
			}
		}
		runWAF(wafCtx, values, &attacks)
	})
}

func runWAF(wafCtx *wafContext, values map[string]interface{}, attacks *[]waftypes.RawAttackMetadata) {
	matches, err := wafCtx.run(values, 4*time.Millisecond)
	if err != nil {
		log.Error("appsec: waf error: %v", err)
		return
	}
	if len(matches) == 0 {
		return
	}
	*attacks = append(*attacks, waftypes.RawAttackMetadata{Time: time.Now(), Metadata: matches})
}

// Rule addresses currently supported by the WAF
const (
	serverRequestRawURIAddr           = "server.request.uri.raw"
	serverRequestHeadersNoCookiesAddr = "server.request.headers.no_cookies"
	serverRequestCookiesAddr          = "server.request.cookies"
	serverRequestQueryAddr            = "server.request.query"
)

// List of rule addresses currently supported by the WAF
var supportedAddressesList = []string{
	serverRequestRawURIAddr,
	serverRequestHeadersNoCookiesAddr,
	serverRequestCookiesAddr,
	serverRequestQueryAddr,
}

func init() {
	sort.Strings(supportedAddressesList)
}

// supportedAddresses returns the list of addresses we actually support from the
// given rule addresses.
func supportedAddresses(ruleAddresses []string) (supported, notSupported []string) {
	// Filter the supported addresses only
	l := len(supportedAddressesList)
	supported = make([]string, 0, l)
	for _, addr := range ruleAddresses {
		if i := sort.SearchStrings(supportedAddressesList, addr); i < l && supportedAddressesList[i] == addr {
			supported = append(supported, addr)
		} else {
			notSupported = append(notSupported, addr)
		}
	}
	// Check the resulting situation we are in
	return supported, notSupported
}
