// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.

package tracer

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/version"
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
	req, err := http.NewRequest("POST", fmt.Sprintf("http://%s/v0.4/traces", resolveAddr(t.config.agentAddr)), strings.NewReader("[]"))
	if err != nil {
		return false, fmt.Errorf("cannot create http request: %v", err)
	}

	req.Header.Set(traceCountHeader, "0")
	req.Header.Set("Content-Length", "2")
	c := &http.Client{}
	response, err := c.Do(req)
	if err != nil {
		return false, err
	}
	if code := response.StatusCode; code != 200 {
		// error, check the body for context information and
		// return a nice error.
		msg := make([]byte, 1000)
		n, _ := response.Body.Read(msg)
		response.Body.Close()
		txt := http.StatusText(code)
		if n > 0 {
			return false, fmt.Errorf("%s (Status: %s)", msg[:n], txt)
		}
		return false, fmt.Errorf("%s", txt)
	}
	return true, nil
}

func newStartupInfo(t *tracer) *startupInfo {
	if startupLogs := os.Getenv("DD_TRACE_STARTUP_LOGS"); startupLogs == "0" {
		return &startupInfo{}
	}
	reachable, reachableErr := agentReachable(t)
	return &startupInfo{
		Date:                  time.Now().Format(time.RFC3339),
		OSName:                osName(),
		OSVersion:             osVersion(),
		Version:               version.Tag,
		Lang:                  "Go",
		LangVersion:           runtime.Version(),
		Env:                   t.config.env,
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
	if startupLogs := os.Getenv("DD_TRACE_STARTUP_LOGS"); startupLogs == "0" {
		return
	}
	bs, err := json.Marshal(info)
	if err != nil {
		log.Error("Failed to serialize json for startup log: %#v\n", info)
		return
	}
	log.Warn("Startup: %s\n", string(bs))
}
