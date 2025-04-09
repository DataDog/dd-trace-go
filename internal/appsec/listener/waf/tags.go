// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package waf

import (
	"time"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/trace"
	"github.com/DataDog/dd-trace-go/v2/internal"
	emitter "github.com/DataDog/dd-trace-go/v2/internal/appsec/emitter/waf"
	"github.com/DataDog/dd-trace-go/v2/internal/samplernames"
	"github.com/DataDog/go-libddwaf/v4"
)

const (
	wafSpanTagPrefix     = "_dd.appsec."
	eventRulesVersionTag = wafSpanTagPrefix + "event_rules.version"
	wafVersionTag        = wafSpanTagPrefix + "waf.version"
	wafErrorTag          = wafSpanTagPrefix + "waf.error"
	wafTimeoutTag        = wafSpanTagPrefix + "waf.timeouts"
	raspRuleEvalTag      = wafSpanTagPrefix + "rasp.rule.eval"
	raspErrorTag         = wafSpanTagPrefix + "rasp.error"
	raspTimeoutTag       = wafSpanTagPrefix + "rasp.timeout"
	truncationTagPrefix  = wafSpanTagPrefix + "truncated."

	blockedRequestTag = "appsec.blocked"
)

// AddRulesMonitoringTags adds the tags related to security rules monitoring
func AddRulesMonitoringTags(th trace.TagSetter) {
	th.SetTag(wafVersionTag, libddwaf.Version())
	th.SetTag(ext.ManualKeep, samplernames.AppSec)
}

// AddWAFMonitoringTags adds the tags related to the monitoring of the WAF
func AddWAFMonitoringTags(th trace.TagSetter, metrics *emitter.ContextMetrics, rulesVersion string, stats libddwaf.Stats) {
	// Rules version is set for every request to help the backend associate Feature duration metrics with rule version
	th.SetTag(eventRulesVersionTag, rulesVersion)

	if raspCallsCount := metrics.SumRASPCalls.Load(); raspCallsCount > 0 {
		th.SetTag(raspRuleEvalTag, raspCallsCount)
	}

	if raspErrorsCount := metrics.SumRASPErrors.Load(); raspErrorsCount > 0 {
		th.SetTag(raspErrorTag, raspErrorsCount)
	}

	if wafErrorsCount := metrics.SumWAFErrors.Load(); wafErrorsCount > 0 {
		th.SetTag(wafErrorTag, wafErrorsCount)
	}

	// Add metrics like `waf.duration` and `rasp.duration_ext`
	for key, value := range stats.Timers {
		th.SetTag(wafSpanTagPrefix+key, float64(value.Nanoseconds())/float64(time.Microsecond.Nanoseconds()))
	}

	if stats.TimeoutCount > 0 {
		th.SetTag(wafTimeoutTag, stats.TimeoutCount)
	}

	if stats.TimeoutRASPCount > 0 {
		th.SetTag(raspTimeoutTag, stats.TimeoutRASPCount)
	}

	addTruncationTags(th, stats)
}

// addTruncationTags adds the span tags related to the truncations
func addTruncationTags(th trace.TagSetter, stats libddwaf.Stats) {
	wafMaxTruncationsMapSize := max(len(stats.Truncations), len(stats.TruncationsRASP))
	if wafMaxTruncationsMapSize == 0 {
		return
	}

	wafMaxTruncationsMap := make(map[libddwaf.TruncationReason]int, wafMaxTruncationsMapSize)
	for reason, list := range stats.Truncations {
		wafMaxTruncationsMap[reason] = max(0, len(list))
	}

	for reason, list := range stats.TruncationsRASP {
		wafMaxTruncationsMap[reason] = max(wafMaxTruncationsMap[reason], len(list))
	}

	for reason, count := range wafMaxTruncationsMap {
		if count > 0 {
			th.SetTag(truncationTagPrefix+reason.String(), count)
		}
	}
}

// SetEventSpanTags sets the security event span tags related to an appsec event
func SetEventSpanTags(span trace.TagSetter) {
	// Keep this span due to the security event
	//
	// This is a workaround to tell the tracer that the trace was kept by AppSec.
	// Passing any other value than `appsec.SamplerAppSec` has no effect.
	// Customers should use `span.SetTag(ext.ManualKeep, true)` pattern
	// to keep the trace, manually.
	span.SetTag(ext.ManualKeep, samplernames.AppSec)
	span.SetTag("_dd.origin", "appsec")
	// Set the appsec.event tag needed by the appsec backend
	span.SetTag("appsec.event", true)
	span.SetTag("_dd.p.ts", internal.TraceSourceTagValue{Value: internal.ASMTraceSource})
}
