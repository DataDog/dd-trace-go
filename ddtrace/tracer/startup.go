// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.

package tracer

import (
	"encoding/json"
	"fmt"
	"math"
	"runtime"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/version"
		"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

type startupInfo struct {
	// Common startup status
	Date                  string                 `json:"date"`
	OSName                string                 `json:"os_name"`
	OSVersion             string                 `json:"os_version"`
	Version               string                 `json:"version"`
	Lang                  string                 `json:"lang"`
	LangVersion           string                 `json:"lang_version"`
	Env                   string                 `json:"env"`
	Service               string                 `json:"service"`
	AgentURL              string                 `json:"agent_url"`
	AgentReachable        bool                   `json:"agent_reachable"`
	AgentError            error                  `json:"agent_error"`
	Debug                 bool                   `json:"debug"`
	AnalyticsEnabled      bool                   `json:"analytics_enabled"`
	SampleRate            float64                `json:"sample_rate"`
	SamplingRules         []SamplingRule         `json:"sampling_rules"`
	SamplingRulesError    error                  `json:"sampling_rules_error"`
	Tags                  map[string]interface{} `json:"tags"`
	RuntimeMetricsEnabled bool                   `json:"runtime_metrics_enabled"`

	//Go-tracer-specific startup status
	GlobalService string `json:"global_service"`
}

func agentReachable(t *tracer) (bool, error) {
	// TODO
	return false, nil
}

func newStartupInfo(t *tracer) *startupInfo {
	reachable, reachableErr := agentReachable(t)
	return &startupInfo{
		Date:                  time.Now().Format("2006-01-02 15:04:05"),
		OSName:                osName(),
		OSVersion:             osVersion(),
		Version:               version.Tag,
		Lang:                  "Go",
		LangVersion:           runtime.Version(),
		Env:                   "TODO: #675",
		Service:               t.config.serviceName,
		AgentURL:              t.config.agentAddr,
		AgentReachable:        reachable,
		AgentError:            reachableErr,
		Debug:                 t.config.debug,
		AnalyticsEnabled:      globalconfig.AnalyticsRate() != math.NaN(),
		SampleRate:            t.prioritySampling.defaultRate,
		SamplingRules:         t.rulesSampling.rules,
		SamplingRulesError:    nil,
		Tags:                  t.globalTags,
		RuntimeMetricsEnabled: t.config.runtimeMetrics,
		GlobalService:         globalconfig.ServiceName(),
	}
}

func logStartup(info *startupInfo) {
	bs, err := json.Marshal(info)
	if err != nil {
		fmt.Printf("Failed to serialize json for startup log: %#v\n", info)
		return
	}
	log.Warn("Startup: %s\n", string(bs))
}
