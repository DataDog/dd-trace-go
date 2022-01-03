package pipelines

import (
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"sync"
)

var (
	mu             sync.RWMutex
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

func Start(opts ...StartOption) {
	cfg := newConfig(opts...)
	p := newProcessor(cfg.statsd, cfg.env, cfg.version, cfg.agentAddr, cfg.httpClient, cfg.ddSite, cfg.apiKey)
	p.Start()
	setGlobalProcessor(p)
}

func Stop() {
	p := getGlobalProcessor()
	if p == nil {
		log.Error("Stopped processor more than once.")
	}
	p.Stop()
	setGlobalProcessor(nil)
}