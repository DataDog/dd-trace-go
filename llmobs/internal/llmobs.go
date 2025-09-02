// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package internal

import (
	"errors"
	"slices"
	"sync"
	"unicode"
)

var (
	mu           sync.Mutex
	activeLLMObs *LLMObs
)

type LLMObs struct {
	Config    *Config
	DNEClient *DNEClient
}

var (
	errLLMObsNotEnabled        = errors.New("LLMObs is not enabled. Ensure the experiment has been correctly initialized using experiment.New and llmobs.Start() has been called or set DD_LLMOBS_ENABLED=1")
	errAgentlessRequiresAPIKey = errors.New("LLMOBs agentless mode requires a valid API key - set the DD_API_KEY env variable to configure one")
)

// See: https://docs.datadoghq.com/getting_started/site/#access-the-datadog-site
var ddSitesNeedingAppSubdomain = []string{"datadoghq.com", "datadoghq.eu", "ddog-gov.com"}

func newLLMObs(opts ...Option) (*LLMObs, error) {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(cfg)
	}
	if cfg.AgentlessEnabled && !isAPIKeyValid(cfg.APIKey) {
		return nil, errAgentlessRequiresAPIKey
	}
	return &LLMObs{Config: cfg, DNEClient: newDNEClient(cfg)}, nil
}

func Start(opts ...Option) error {
	mu.Lock()
	defer mu.Unlock()

	if activeLLMObs != nil {
		activeLLMObs.Stop()
	}
	l, err := newLLMObs(opts...)
	if err != nil {
		return err
	}
	if !l.Config.Enabled {
		return nil
	}
	activeLLMObs = l
	activeLLMObs.Run()
	return nil
}

func Stop() {
	mu.Lock()
	defer mu.Unlock()

	if activeLLMObs != nil {
		activeLLMObs.Stop()
		activeLLMObs = nil
	}
}

func ActiveLLMObs() (*LLMObs, error) {
	if activeLLMObs == nil || !activeLLMObs.Config.Enabled {
		return nil, errLLMObsNotEnabled
	}
	return activeLLMObs, nil
}

func (l *LLMObs) Stop() {
	//TODO
}

func (l *LLMObs) Run() {
	//TODO
}

func ResourceBaseURL() string {
	site := defaultSite
	if activeLLMObs != nil {
		site = activeLLMObs.Config.Site
	}

	baseURL := "https://"
	if slices.Contains(ddSitesNeedingAppSubdomain, site) {
		baseURL += "app."
	}
	baseURL += site
	return baseURL
}

// isAPIKeyValid reports whether the given string is a structurally valid API key
func isAPIKeyValid(key string) bool {
	if len(key) != 32 {
		return false
	}
	for _, c := range key {
		if c > unicode.MaxASCII || (!unicode.IsLower(c) && !unicode.IsNumber(c)) {
			return false
		}
	}
	return true
}
