// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package waf

import (
	"encoding/json"

	waf "github.com/DataDog/go-libddwaf/v3"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/trace"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/samplernames"
)

const (
	eventRulesVersionTag = "_dd.appsec.event_rules.version"
	eventRulesErrorsTag  = "_dd.appsec.event_rules.errors"
	eventRulesLoadedTag  = "_dd.appsec.event_rules.loaded"
	eventRulesFailedTag  = "_dd.appsec.event_rules.error_count"
	wafVersionTag        = "_dd.appsec.waf.version"
)

// AddRulesMonitoringTags adds the tags related to security rules monitoring
func AddRulesMonitoringTags(th trace.TagSetter, wafDiags waf.Diagnostics) {
	rInfo := wafDiags.Rules
	if rInfo == nil {
		return
	}

	if len(rInfo.Errors) == 0 {
		rInfo.Errors = nil
	}
	rulesetErrors, err := json.Marshal(wafDiags.Rules.Errors)
	if err != nil {
		log.Error("appsec: could not marshal the waf ruleset info errors to json")
	}
	th.SetTag(eventRulesErrorsTag, string(rulesetErrors)) // avoid the tracer's call to fmt.Sprintf on the value
	th.SetTag(eventRulesLoadedTag, len(rInfo.Loaded))
	th.SetTag(eventRulesFailedTag, len(rInfo.Failed))
	th.SetTag(wafVersionTag, waf.Version())
	th.SetTag(ext.ManualKeep, samplernames.AppSec)
}

// AddWAFMonitoringTags adds the tags related to the monitoring of the WAF
func AddWAFMonitoringTags(th trace.TagSetter, rulesVersion string, stats map[string]any) {
	// Rules version is set for every request to help the backend associate WAF duration metrics with rule version
	th.SetTag(eventRulesVersionTag, rulesVersion)

	// Report the stats sent by the WAF
	for k, v := range stats {
		th.SetTag(k, v)
	}
}
