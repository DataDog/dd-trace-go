// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

//go:build appsec
// +build appsec

package appsec

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo/instrumentation/httpsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/waf"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

// Register the WAF event listener.
func registerWAF(rules []byte, timeout time.Duration) (unreg dyngo.UnregisterFunc, err error) {
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
	unregister := dyngo.Register(newWAFEventListener(waf, addresses, timeout))
	// Return an unregistration function that will also release the WAF instance.
	return func() {
		defer waf.Close()
		unregister()
	}, nil
}

// newWAFEventListener returns the WAF event listener to register in order to enable it.
func newWAFEventListener(handle *waf.Handle, addresses []string, timeout time.Duration) dyngo.EventListener {
	return httpsec.OnHandlerOperationStart(func(op *httpsec.Operation, args httpsec.HandlerOperationArgs) {
		// At the moment, AppSec doesn't block the requests, and so we can use the fact we are in monitoring-only mode
		// to call the WAF only once at the end of the handler operation.
		op.On(httpsec.OnHandlerOperationFinish(func(op *httpsec.Operation, res httpsec.HandlerOperationRes) {
			wafCtx := waf.NewContext(handle)
			if wafCtx == nil {
				// The WAF event listener got concurrently released
				return
			}
			defer wafCtx.Close()

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
				case serverResponseStatusAddr:
					values[serverResponseStatusAddr] = res.Status
				}
			}
			matches := runWAF(wafCtx, values, timeout)

			// Log the attacks if any
			if len(matches) == 0 {
				return
			}
			log.Debug("appsec: attack detected by the waf")
			op.AddSecurityEvent(matches)
		}))

	})
}

func runWAF(wafCtx *waf.Context, values map[string]interface{}, timeout time.Duration) []byte {
	matches, err := wafCtx.Run(values, timeout)
	if err != nil {
		if err == waf.ErrTimeout {
			log.Debug("appsec: waf timeout value of %s reached", timeout)
		} else {
			log.Error("appsec: unexpected waf error: %v", err)
			return nil
		}
	}
	return matches
}

// Rule addresses currently supported by the WAF
const (
	serverRequestRawURIAddr           = "server.request.uri.raw"
	serverRequestHeadersNoCookiesAddr = "server.request.headers.no_cookies"
	serverRequestCookiesAddr          = "server.request.cookies"
	serverRequestQueryAddr            = "server.request.query"

	serverResponseStatusAddr = "server.response.status"
)

// List of rule addresses currently supported by the WAF
var supportedAddressesList = []string{
	serverRequestRawURIAddr,
	serverRequestHeadersNoCookiesAddr,
	serverRequestCookiesAddr,
	serverRequestQueryAddr,
	serverResponseStatusAddr,
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
