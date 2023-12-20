// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sharedsec

import (
	"encoding/json"
	"time"

	"github.com/DataDog/appsec-internal-go/limiter"
	waf "github.com/DataDog/go-libddwaf/v2"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/sharedsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/trace"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

func RunWAF(wafCtx *waf.Context, values waf.RunAddressData, timeout time.Duration) waf.Result {
	result, err := wafCtx.Run(values, timeout)
	if err == waf.ErrTimeout {
		log.Debug("appsec: waf timeout value of %s reached", timeout)
	} else if err != nil {
		log.Error("appsec: unexpected waf error: %v", err)
	}
	return result
}

type securityEventsAdder interface {
	AddSecurityEvents(events []any)
}

// Helper function to add sec events to an operation taking into account the rate limiter.
func AddSecurityEvents(op securityEventsAdder, limiter limiter.Limiter, matches []any) {
	if len(matches) > 0 && limiter.Allow() {
		op.AddSecurityEvents(matches)
	}
}

const (
	eventRulesVersionTag = "_dd.appsec.event_rules.version"
	eventRulesErrorsTag  = "_dd.appsec.event_rules.errors"
	eventRulesLoadedTag  = "_dd.appsec.event_rules.loaded"
	eventRulesFailedTag  = "_dd.appsec.event_rules.error_count"
	wafDurationTag       = "_dd.appsec.waf.duration"
	wafDurationExtTag    = "_dd.appsec.waf.duration_ext"
	wafTimeoutTag        = "_dd.appsec.waf.timeouts"
	wafVersionTag        = "_dd.appsec.waf.version"
)

// Add the tags related to security rules monitoring
func AddRulesMonitoringTags(th trace.TagSetter, wafDiags *waf.Diagnostics) {
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
}

// Add the tags related to the monitoring of the WAF
func AddWAFMonitoringTags(th trace.TagSetter, rulesVersion string, overallRuntimeNs, internalRuntimeNs, timeouts uint64) {
	// Rules version is set for every request to help the backend associate WAF duration metrics with rule version
	th.SetTag(eventRulesVersionTag, rulesVersion)
	th.SetTag(wafTimeoutTag, timeouts)
	th.SetTag(wafDurationTag, float64(internalRuntimeNs)/1e3)   // ns to us
	th.SetTag(wafDurationExtTag, float64(overallRuntimeNs)/1e3) // ns to us
}

// ProcessActions sends the relevant actions to the operation's data listener.
// It returns true if at least one of those actions require interrupting the request handler
func ProcessActions(op dyngo.Operation, actions sharedsec.Actions, actionIds []string) (interrupt bool) {
	for _, id := range actionIds {
		if a, ok := actions[id]; ok {
			dyngo.EmitData(op, actions[id])
			interrupt = interrupt || a.Blocking()
		}
	}
	return interrupt
}
