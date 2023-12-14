// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package appsec

import (
	"errors"

	"github.com/DataDog/appsec-internal-go/limiter"
	waf "github.com/DataDog/go-libddwaf/v2"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/sharedsec"
	grpc "gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/listener/grpcsec"
	http "gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/listener/httpsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

const (
	eventRulesVersionTag = "_dd.appsec.event_rules.version"
	eventRulesErrorsTag  = "_dd.appsec.event_rules.errors"
	eventRulesLoadedTag  = "_dd.appsec.event_rules.loaded"
	eventRulesFailedTag  = "_dd.appsec.event_rules.error_count"
	wafDurationTag       = "_dd.appsec.waf.duration"
	wafDurationExtTag    = "_dd.appsec.waf.duration_ext"
	wafTimeoutTag        = "_dd.appsec.waf.timeouts"
	wafVersionTag        = "_dd.appsec.waf.version"
)

type wafHandle struct {
	*waf.Handle
	// actions are tightly link to a ruleset, which is linked to a waf handle
	actions sharedsec.Actions
}

func (a *appsec) swapWAF(rules rulesFragment) (err error) {
	// Instantiate a new WAF handle and verify its state
	newHandle, err := newWAFHandle(rules, a.cfg)
	if err != nil {
		return err
	}

	// Close the WAF handle in case of an error in what's following
	defer func() {
		if err != nil {
			newHandle.Close()
		}
	}()

	listeners, err := newWAFEventListeners(newHandle, a.cfg, a.limiter)
	if err != nil {
		return err
	}

	// Register the event listeners now that we know that the new handle is valid
	newRoot := dyngo.NewRootOperation()
	for _, l := range listeners {
		newRoot.On(l)
	}

	// Hot-swap dyngo's root operation
	dyngo.SwapRootOperation(newRoot)

	// Close old handle.
	// Note that concurrent requests are still using it, and it will be released
	// only when no more requests use it.
	// TODO: implement in dyngo ref-counting of the root operation so we can
	//   rely on a Finish event listener on the root operation instead?
	//   Avoiding saving the current WAF handle would guarantee no one is
	//   accessing a.wafHandle while we swap
	oldHandle := a.wafHandle
	a.wafHandle = newHandle
	if oldHandle != nil {
		oldHandle.Close()
	}

	return nil
}

func actionFromEntry(e *actionEntry) *sharedsec.Action {
	switch e.Type {
	case "block_request":
		grpcCode := 10 // use the grpc.Codes value for "Aborted" by default
		if e.Parameters.GRPCStatusCode != nil {
			grpcCode = *e.Parameters.GRPCStatusCode
		}
		return sharedsec.NewBlockRequestAction(e.Parameters.StatusCode, grpcCode, e.Parameters.Type)
	case "redirect_request":
		return sharedsec.NewRedirectRequestAction(e.Parameters.StatusCode, e.Parameters.Location)
	default:
		log.Debug("appsec: unknown action type `%s`", e.Type)
		return nil
	}
}

func newWAFHandle(rules rulesFragment, cfg *Config) (*wafHandle, error) {
	handle, err := waf.NewHandle(rules, cfg.obfuscator.KeyRegex, cfg.obfuscator.ValueRegex)
	actions := sharedsec.Actions{
		// Default built-in block action
		"block": sharedsec.NewBlockRequestAction(403, 10, "auto"),
	}

	for _, entry := range rules.Actions {
		a := actionFromEntry(&entry)
		if a != nil {
			actions[entry.ID] = a
		}
	}
	return &wafHandle{
		Handle:  handle,
		actions: actions,
	}, err
}

func newWAFEventListeners(waf *wafHandle, cfg *Config, l limiter.Limiter) (listeners []dyngo.EventListener, err error) {
	// Check if there are addresses in the rule
	ruleAddresses := waf.Addresses()
	if len(ruleAddresses) == 0 {
		return nil, errors.New("no addresses found in the rule")
	}

	// Check which addresses are supported by what listener
	httpAddresses := make(map[string]struct{}, len(ruleAddresses))
	grpcAddresses := make(map[string]struct{}, len(ruleAddresses))
	notSupported := make([]string, 0, len(ruleAddresses))
	for _, address := range ruleAddresses {
		supported := false
		if http.SupportsAddress(address) {
			httpAddresses[address] = struct{}{}
			supported = true
		}
		if grpc.SupportsAddress(address) {
			grpcAddresses[address] = struct{}{}
			supported = true
		}
		if !supported {
			notSupported = append(notSupported, address)
		}
	}

	if len(notSupported) > 0 {
		log.Debug("appsec: the addresses present in the rules are partially supported: not supported=%v", notSupported)
	}

	// Register the WAF event listeners
	if len(httpAddresses) > 0 {
		log.Debug("appsec: creating http waf event listener of the rules addresses %v", httpAddresses)
		listeners = append(listeners, http.NewWAFEventListener(waf.Handle, waf.actions, httpAddresses, cfg.wafTimeout, &cfg.apiSec, l))
	}

	if len(grpcAddresses) > 0 {
		log.Debug("appsec: creating the grpc waf event listener of the rules addresses %v", grpcAddresses)
		listeners = append(listeners, grpc.NewWAFEventListener(waf.Handle, waf.actions, grpcAddresses, cfg.wafTimeout, l))
	}

	return listeners, nil
}
