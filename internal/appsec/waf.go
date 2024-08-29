// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package appsec

import (
	"fmt"
	"sync"
	"time"

	"github.com/DataDog/appsec-internal-go/limiter"
	waf "github.com/DataDog/go-libddwaf/v3"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/config"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/listener/wafsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

type WAF struct {
	timeout         time.Duration
	limiter         *limiter.TokenTicker
	handle          *waf.Handle
	reportRulesTags sync.Once
}

func NewWAF(cfg *config.Config, rootOp dyngo.Operation) (Product, error) {
	if ok, err := waf.Load(); err != nil {
		// 1. If there is an error and the loading is not ok: log as an unexpected error case and quit appsec
		// Note that we assume here that the test for the unsupported target has been done before calling
		// this method, so it is now considered an error for this method
		if !ok {
			return nil, fmt.Errorf("error while loading libddwaf: %w", err)
		}
		// 2. If there is an error and the loading is ok: log as an informative error where appsec can be used
		log.Error("appsec: non-critical error while loading libddwaf: %v", err)
	}

	newHandle, err := waf.NewHandle(cfg.RulesManager.Latest, cfg.Obfuscator.KeyRegex, cfg.Obfuscator.ValueRegex)
	if err != nil {
		return nil, err
	}

	tokenTicker := limiter.NewTokenTicker(cfg.TraceRateLimit, cfg.TraceRateLimit)
	tokenTicker.Start()

	// TODO: register wafsec context listener (makw sure to close the WAF handle if errors happens)

	dyngo.On(rootOp, wafsec.OnStart)
	dyngo.OnFinish(rootOp, wafsec.OnFinish)

	return &WAF{
		handle:  newHandle,
		timeout: cfg.WAFTimeout,
		limiter: tokenTicker,
	}, nil
}

func (waf *WAF) Stop() {
	waf.limiter.Stop()
	waf.handle.Close()
}
