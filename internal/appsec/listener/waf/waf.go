// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package waf

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/DataDog/appsec-internal-go/limiter"
	wafv3 "github.com/DataDog/go-libddwaf/v3"
	wafErrors "github.com/DataDog/go-libddwaf/v3/errors"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/config"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/wafsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/listener/sharedsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/trace"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

type WAF struct {
	timeout         time.Duration
	limiter         *limiter.TokenTicker
	handle          *wafv3.Handle
	reportRulesTags sync.Once
}

func NewWAF(cfg *config.Config, rootOp dyngo.Operation) (func(), error) {
	if ok, err := wafv3.Load(); err != nil {
		// 1. If there is an error and the loading is not ok: log as an unexpected error case and quit appsec
		// Note that we assume here that the test for the unsupported target has been done before calling
		// this method, so it is now considered an error for this method
		if !ok {
			return nil, fmt.Errorf("error while loading libddwaf: %w", err)
		}
		// 2. If there is an error and the loading is ok: log as an informative error where appsec can be used
		log.Error("appsec: non-critical error while loading libddwaf: %v", err)
	}

	newHandle, err := wafv3.NewHandle(cfg.RulesManager.Latest, cfg.Obfuscator.KeyRegex, cfg.Obfuscator.ValueRegex)
	if err != nil {
		return nil, err
	}

	tokenTicker := limiter.NewTokenTicker(cfg.TraceRateLimit, cfg.TraceRateLimit)
	tokenTicker.Start()

	waf := &WAF{
		handle:  newHandle,
		timeout: cfg.WAFTimeout,
		limiter: tokenTicker,
	}

	dyngo.On(rootOp, waf.onStart)
	dyngo.OnFinish(rootOp, waf.onFinish)

	return waf.Stop, nil
}

func (waf *WAF) onStart(op *wafsec.WAFContextOperation, _ wafsec.WAFContextArgs) {
	ctx, err := waf.handle.NewContextWithBudget(waf.timeout)
	if err != nil {
		log.Debug("appsec: failed to create WAF context: %v", err)
	}

	op.Context.Store(ctx)

	waf.reportRulesTags.Do(func() {
		AddRulesMonitoringTags(op, waf.handle.Diagnostics())
	})

	dyngo.OnData(op, func(data wafv3.RunAddressData) {
		waf.run(op, data)
	})
}

func (waf *WAF) run(op *wafsec.WAFContextOperation, data wafv3.RunAddressData) {
	ctx := op.Context.Load()
	if ctx == nil {
		return
	}

	result, err := ctx.Run(data)
	if errors.Is(err, wafErrors.ErrTimeout) {
		log.Debug("appsec: waf timeout value reached: %v", err)
	} else if err != nil {
		log.Error("appsec: unexpected waf error: %v", err)
	}

	if !result.HasEvents() {
		return
	}

	log.Debug("appsec: WAF detected a suspicious WAF event")
	sharedsec.ProcessActions(op, result.Actions)

	if waf.limiter.Allow() {
		log.Warn("appsec: too many WAF events, stopping further processing")
		return
	}

	op.AddEvents(result.Events)
}

func (waf *WAF) onFinish(op *wafsec.WAFContextOperation, _ wafsec.WAFContextRes) {
	ctx := op.Context.Swap(nil)
	if ctx == nil {
		return
	}

	ctx.Close()

	AddWAFMonitoringTags(op, waf.handle.Diagnostics().Version, ctx.Stats().Metrics())
	if err := trace.SetEventSpanTags(op, op.Events()); err != nil {
		log.Debug("appsec: failed to set event span tags: %v", err)
	}
}

func (waf *WAF) Stop() {
	waf.limiter.Stop()
	waf.handle.Close()
}
