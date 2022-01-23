// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package pipelines

import (
	"log"
	"sync"
)

var (
	mu              sync.RWMutex
	activeProcessor *processor
)

func setGlobalProcessor(p *processor) {
	mu.Lock()
	defer mu.Unlock()
	activeProcessor = p
}

func getGlobalProcessor() *processor {
	mu.RLock()
	defer mu.RUnlock()
	return activeProcessor
}

// Start starts the pipeline processor that will record pipeline stats and send them to the agent.
func Start(opts ...StartOption) {
	cfg := newConfig(opts...)
	if !cfg.agentLess && !cfg.features.PipelineStats {
		log.Print("ERROR: Agent does not support pipeline stats and pipeline stats processor launched in agent mode.")
		return
	}
	p := newProcessor(cfg.statsd, cfg.env, cfg.service, cfg.agentAddr, cfg.httpClient, cfg.site, cfg.apiKey, cfg.agentLess)
	p.Start()
	setGlobalProcessor(p)
}

// Stop stops the pipeline processor.
func Stop() {
	p := getGlobalProcessor()
	if p == nil {
		log.Print("ERROR: Stopped processor more than once.")
	}
	p.Stop()
	setGlobalProcessor(nil)
}
