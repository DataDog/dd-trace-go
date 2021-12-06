// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

//go:build appsec
// +build appsec

package appsec

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo/instrumentation/httpsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/waf"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

// wafEvent is the raw attack event returned by the WAF when matching.
type wafEvent struct {
	time time.Time
	// metadata is the raw JSON representation of the attackMetadata slice.
	metadata []byte
}

// Register the WAF event listener.
func registerWAF(rules []byte, timeout time.Duration, appsec *appsec) (unreg dyngo.UnregisterFunc, err error) {
	// Check the WAF is healthy
	if _, err := waf.Health(); err != nil {
		return nil, err
	}

	// Instantiate the WAF
	if rules == nil {
		rules = []byte(staticRecommendedRule)
	}
	waf, err := waf.NewHandle(rules)
	if err != nil {
		return nil, err
	}
	// Close the WAF in case of an error in what's following
	defer func() {
		if err != nil {
			waf.Close()
		}
	}()

	// Check if there are addresses in the rule
	ruleAddresses := waf.Addresses()
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
	unregister := dyngo.Register(newWAFEventListener(waf, addresses, appsec, timeout))
	// Return an unregistration function that will also release the WAF instance.
	return func() {
		defer waf.Close()
		unregister()
	}, nil
}

// newWAFEventListener returns the WAF event listener to register in order to enable it.
func newWAFEventListener(handle *waf.Handle, addresses []string, appsec *appsec, timeout time.Duration) dyngo.EventListener {
	return httpsec.OnHandlerOperationStart(func(op dyngo.Operation, args httpsec.HandlerOperationArgs) {
		// For this handler operation lifetime, create a WAF context and the
		// list of detected attacks
		wafCtx := waf.NewContext(handle)
		if wafCtx == nil {
			// The WAF event listener got concurrently released
			return
		}
		// TODO(julio): make it a thread-safe list of security events once we
		//  listen for sub-operations
		var baseEvent *wafEvent

		op.On(httpsec.OnHandlerOperationFinish(func(op dyngo.Operation, res httpsec.HandlerOperationRes) {
			// Release the WAF context
			wafCtx.Close()
			// Log the attacks if any
			if baseEvent == nil {
				return
			}
			log.Debug("appsec: attack detected by the waf")

			// Create the base security event out of the slide of attacks
			event := withHTTPOperationContext(baseEvent, args, res)

			// Check if a span exists
			if span := args.Span; span != nil {
				// Add the span context to the security event
				spanCtx := span.Context()
				event = withSpanContext(event, spanCtx.TraceID(), spanCtx.SpanID())
				// Keep this span due to the security event
				span.SetTag(ext.ManualKeep, true)
				// Set the appsec.event tag needed by the appsec backend
				span.SetTag("appsec.event", true)
			}
			appsec.sendEvent(event)
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
		baseEvent = runWAF(wafCtx, values, timeout)
	})
}

func runWAF(wafCtx *waf.Context, values map[string]interface{}, timeout time.Duration) *wafEvent {
	matches, err := wafCtx.Run(values, timeout)
	if err != nil {
		log.Error("appsec: waf error: %v", err)
		return nil
	}
	if len(matches) == 0 {
		return nil
	}
	return &wafEvent{
		time:     time.Now(),
		metadata: matches,
	}
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

// toIntakeEvent creates the attack event payloads from a WAF attack.
func (e *wafEvent) toIntakeEvents() (events []*attackEvent, err error) {
	var matches waf.AttackMetadata
	if err := json.Unmarshal(e.metadata, &matches); err != nil {
		return nil, err
	}
	// Create one security event per flow and per filter
	for _, match := range matches {
		for _, filter := range match.Filter {
			ruleMatch := &attackRuleMatch{
				Operator:      filter.Operator,
				OperatorValue: filter.OperatorValue,
				Parameters: []attackRuleMatchParameter{
					{
						Name:  filter.ManifestKey,
						Value: filter.ResolvedValue,
					},
				},
				Highlight: []string{filter.MatchStatus},
			}
			events = append(events, newAttackEvent(match.Rule, match.Flow, match.Flow, e.time, ruleMatch))
		}
	}
	return events, nil
}
