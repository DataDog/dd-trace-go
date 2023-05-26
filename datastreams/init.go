// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

// this package will later be copied to dd-trace-go once we end the experimentation on it.
// So it can't use any dd-go dependencies.

package datastreams

import (
	"log"
	"sync"
)

var (
	mu               sync.RWMutex
	activeAggregator *aggregator
)

func setGlobalAggregator(a *aggregator) {
	mu.Lock()
	defer mu.Unlock()
	old := activeAggregator
	activeAggregator = a
	if old != nil {
		old.Stop()
	}
}

func getGlobalAggregator() *aggregator {
	mu.RLock()
	defer mu.RUnlock()
	return activeAggregator
}

// Start starts the data streams stats aggregator that will record pipeline stats and send them to the agent.
func Start(opts ...StartOption) {
	cfg := newConfig(opts...)
	if !cfg.agentLess && !cfg.features.PipelineStats {
		log.Print("ERROR: Agent does not support pipeline stats and pipeline stats aggregator launched in agent mode.")
		return
	}
	p := newAggregator(cfg.statsd, cfg.env, cfg.primaryTag, cfg.service, cfg.agentAddr, cfg.httpClient, cfg.site, cfg.apiKey, cfg.agentLess)
	p.Start()
	setGlobalAggregator(p)
}

// Stop stops the data streams stats aggregator.
func Stop() {
	p := getGlobalAggregator()
	if p == nil {
		log.Print("ERROR: Stopped aggregator more than once.")
		return
	}
	p.Stop()
	setGlobalAggregator(nil)
}
