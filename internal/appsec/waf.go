// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package appsec

import (
	"github.com/DataDog/appsec-internal-go/limiter"
	waf "github.com/DataDog/go-libddwaf/v3"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/config"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
)

func (a *appsec) swapWAF(rules config.RulesFragment) (err error) {
	// Instantiate a new WAF handle and verify its state
	newHandle, err := waf.NewHandle(rules, a.cfg.Obfuscator.KeyRegex, a.cfg.Obfuscator.ValueRegex)
	if err != nil {
		return err
	}

	// Close the WAF handle in case of an error in what's following
	defer func() {
		if err != nil {
			newHandle.Close()
		}
	}()

	newRoot := dyngo.NewRootOperation()
	for _, fn := range wafEventListeners {
		fn(newHandle, a.cfg, a.limiter, newRoot)
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

type wafEventListener func(*waf.Handle, *config.Config, limiter.Limiter, dyngo.Operation)

// wafEventListeners is the global list of event listeners registered by contribs at init time. This
// is thread-safe assuming all writes (via AddWAFEventListener) are performed within `init`
// functions; so this is written to only during initialization, and is read from concurrently only
// during runtime when no writes are happening anymore.
var wafEventListeners []wafEventListener

// AddWAFEventListener adds a new WAF event listener to be registered whenever a new root operation
// is created. The normal way to use this is to call it from a `func init() {}` so that it is
// guaranteed to have happened before any listened to event may be emitted.
func AddWAFEventListener(fn wafEventListener) {
	wafEventListeners = append(wafEventListeners, fn)
}
