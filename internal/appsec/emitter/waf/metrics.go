// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package waf

import (
	"errors"
	"strconv"
	"sync"

	waf "github.com/DataDog/go-libddwaf/v3"
	wafErrors "github.com/DataDog/go-libddwaf/v3/errors"

	"gopkg.in/DataDog/dd-trace-go.v1/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/waf/addresses"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"
	telemetrylog "gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry/log"
)

// newHandleTelemetryMetric is the name of the metric that will be used to track the initialization of the WAF handle
// this values is changed to waf.updates after the first call to [NewMetricsInstance]
var newHandleTelemetryMetric = "waf.init"
var changeToWafUpdates sync.Once

type CountWafRequests struct {
	requestBlocked bool
	ruleTriggered  bool
	wafTimeout     bool
	rateLimited    bool
	wafError       bool
}

// Metrics is a struct that holds all the telemetry metrics for the WAF that lives and die alongside with the WAF handle
type Metrics struct {
	baseTags     []string
	baseRASPTags map[addresses.RASPRuleType][]string

	// truncationCounts holds the telemetry metrics for the `waf.input_truncated` metric
	truncationCounts map[waf.TruncationReason]telemetry.MetricHandle
	// truncationDistributions holds the telemetry metrics for the `waf.truncated_value_size` metric
	truncationDistributions map[waf.TruncationReason]telemetry.MetricHandle
	// wafRunMetrics holds the telemetry metrics for the `rasp.timeout`, `rasp.duration`, `rasp.duration_ext`, `waf.duration`, `waf.duration_ext` metrics
	// than are returned as-is by go-libddwaf
	wafRunMetrics map[string]telemetry.MetricHandle

	// wafRequestsCounts holds the telemetry metrics for the `waf.requests` metric
	wafRequestsCounts map[CountWafRequests]telemetry.MetricHandle
	// raspRuleEval holds the telemetry metrics for the `rasp.rule_eval` metric by rule type
	raspRuleEval map[addresses.RASPRuleType]telemetry.MetricHandle

	// raspRuleEvalSum is the sum of all the RASP errors that happened during the request
	raspRuleEvalSum int
	// wafErrorsSum is the sum of all the WAF errors that happened during the request
	wafErrorsSum int
	// raspErrorsSum is the sum of all the RASP errors that happened during the request
	raspErrorsSum int
}

var BaseRASPTags = map[addresses.RASPRuleType][]string{
	addresses.RASPRuleTypeLFI:  {"rule_type:" + string(addresses.RASPRuleTypeLFI)},
	addresses.RASPRuleTypeSSRF: {"rule_type:" + string(addresses.RASPRuleTypeSSRF)},
	addresses.RASPRuleTypeSQLI: {"rule_type:" + string(addresses.RASPRuleTypeSQLI)},
	addresses.RASPRuleTypeCMDI: {"rule_type:" + string(addresses.RASPRuleTypeCMDI), "rule_variant:exec"},
}

// NewMetricsInstance creates a new Metrics struct and submit the `waf.init` or `waf.updates` metric. To be called with the raw results of the WAF handle initialization
func NewMetricsInstance(newHandle *waf.Handle, errIn error) Metrics {
	var eventRulesVersion string
	if newHandle != nil {
		eventRulesVersion = newHandle.Diagnostics().Version
	}

	telemetry.Count(telemetry.NamespaceAppSec, newHandleTelemetryMetric, []string{
		"waf_version:" + waf.Version(),
		"event_rules_version:" + eventRulesVersion,
		"success:" + strconv.FormatBool(errIn == nil),
	}).Submit(1)

	changeToWafUpdates.Do(func() {
		newHandleTelemetryMetric = "waf.updates"
	})

	baseTags := []string{
		"event_rules_version:" + eventRulesVersion,
		"waf_version:" + waf.Version(),
	}

	raspTags := make(map[addresses.RASPRuleType][]string, len(addresses.RASPRuleTypes()))
	for _, ruleType := range addresses.RASPRuleTypes() {
		tags := make([]string, len(BaseRASPTags[ruleType])+len(baseTags))
		copy(tags, BaseRASPTags[ruleType])
		copy(tags[len(BaseRASPTags[ruleType]):], baseTags)
		raspTags[ruleType] = tags
	}

	// Build the waf.requests matrix
	// Some actually don't make sense but adding all of them manually would definitely add human mistakes to the mix
	// TODO: add request_excluded and block_failure to the mix once we have the capability to track them
	wafRequestMetrics := make(map[CountWafRequests]telemetry.MetricHandle, 2^5)
	for _, requestBlocked := range []bool{true, false} {
		for _, ruleTriggered := range []bool{true, false} {
			for _, wafTimeout := range []bool{true, false} {
				for _, rateLimited := range []bool{true, false} {
					for _, wafError := range []bool{true, false} {
						wafRequestMetrics[CountWafRequests{
							requestBlocked: requestBlocked,
							ruleTriggered:  ruleTriggered,
							wafTimeout:     wafTimeout,
							rateLimited:    rateLimited,
							wafError:       wafError,
						}] = telemetry.Count(telemetry.NamespaceAppSec, "waf.requests", append([]string{
							"request_blocked:" + strconv.FormatBool(requestBlocked),
							"rule_triggered:" + strconv.FormatBool(ruleTriggered),
							"timeout:" + strconv.FormatBool(wafTimeout),
							"rate_limited:" + strconv.FormatBool(rateLimited),
							"error:" + strconv.FormatBool(wafError),
						}, baseTags...))
					}
				}
			}
		}
	}

	raspRuleEval := make(map[addresses.RASPRuleType]telemetry.MetricHandle, len(addresses.RASPRuleTypes()))
	for _, ruleType := range addresses.RASPRuleTypes() {
		raspRuleEval[ruleType] = telemetry.Count(telemetry.NamespaceAppSec, "rasp.rule.eval", raspTags[ruleType])
	}

	return Metrics{
		baseTags:     baseTags,
		baseRASPTags: raspTags,
		truncationCounts: map[waf.TruncationReason]telemetry.MetricHandle{
			waf.StringTooLong:     telemetry.Count(telemetry.NamespaceAppSec, "waf.input_truncated", []string{"truncation_reason:" + strconv.Itoa(int(waf.StringTooLong))}),
			waf.ContainerTooLarge: telemetry.Count(telemetry.NamespaceAppSec, "waf.input_truncated", []string{"truncation_reason:" + strconv.Itoa(int(waf.ContainerTooLarge))}),
			waf.ObjectTooDeep:     telemetry.Count(telemetry.NamespaceAppSec, "waf.input_truncated", []string{"truncation_reason:" + strconv.Itoa(int(waf.ObjectTooDeep))}),
		},
		truncationDistributions: map[waf.TruncationReason]telemetry.MetricHandle{
			waf.StringTooLong:     telemetry.Count(telemetry.NamespaceAppSec, "waf.truncated_value_size", []string{"truncation_reason:" + strconv.Itoa(int(waf.StringTooLong))}),
			waf.ContainerTooLarge: telemetry.Count(telemetry.NamespaceAppSec, "waf.truncated_value_size", []string{"truncation_reason:" + strconv.Itoa(int(waf.ContainerTooLarge))}),
			waf.ObjectTooDeep:     telemetry.Count(telemetry.NamespaceAppSec, "waf.truncated_value_size", []string{"truncation_reason:" + strconv.Itoa(int(waf.ObjectTooDeep))}),
		},
		wafRunMetrics: map[string]telemetry.MetricHandle{
			"rasp.timeout":      telemetry.Count(telemetry.NamespaceAppSec, "rasp.timeout", baseTags),
			"rasp.duration":     telemetry.Distribution(telemetry.NamespaceAppSec, "rasp.duration", baseTags),
			"rasp.duration_ext": telemetry.Distribution(telemetry.NamespaceAppSec, "rasp.duration_ext", baseTags),
			"waf.duration":      telemetry.Distribution(telemetry.NamespaceAppSec, "waf.duration", baseTags),
			"waf.duration_ext":  telemetry.Distribution(telemetry.NamespaceAppSec, "waf.duration_ext", baseTags),
		},
		wafRequestsCounts: wafRequestMetrics,
		raspRuleEval:      raspRuleEval,
	}
}

// SumRASPCalls returns the sum of all the RASP calls made by the WAF whatever the rasp rule type it is.
func (t *Metrics) SumRASPCalls() float64 {
	return float64(t.raspRuleEvalSum)
}

// SumWAFErrors returns the sum of all the WAF errors
func (t *Metrics) SumWAFErrors() float64 {
	return float64(t.wafErrorsSum)
}

// SumRASPErrors returns the sum of all the RASP errors
func (t *Metrics) SumRASPErrors() float64 {
	return float64(t.raspErrorsSum)
}

// IncWafStats increment the metrics for the WAF run stats at the end of each waf context lifecycle
func (t *Metrics) IncWafStats(stats waf.Stats) {
	for key, value := range stats.Metrics() {
		if metric, ok := t.wafRunMetrics[key]; ok {
			val, ok := internal.ToFloat64(value)
			if !ok {
				telemetrylog.Error("could not convert metric value to float64: %v (of type %T)", value, value, telemetry.WithTags([]string{"product:appsec"}))
				continue
			}
			metric.Submit(val)
		}
	}

	for reason, sizes := range stats.Truncations {
		t.truncationCounts[reason].Submit(1)
		distMetric, ok := t.truncationDistributions[reason]
		if !ok {
			telemetrylog.Error("unexpected truncation reason: %v", reason, telemetry.WithTags([]string{"product:appsec"}))
			continue
		}
		for _, size := range sizes {
			distMetric.Submit(float64(size))
		}
	}
}

// IncWafRequests increment the metric count `waf.requests` with the given tags at the end of each waf run
func (t *Metrics) IncWafRequests(addrs waf.RunAddressData, tags CountWafRequests) {
	switch addrs.Scope {
	case waf.RASPScope:
		t.raspRuleEvalSum++
		ruleType, ok := addresses.RASPRuleTypeFromAddressSet(addrs)
		if !ok {
			telemetrylog.Error("unexpected call to RASPRuleTypeFromAddressSet", telemetry.WithTags([]string{"product:appsec"}))
			return
		}
		if metric, ok := t.raspRuleEval[ruleType]; ok {
			metric.Submit(1)
		}
		if tags.ruleTriggered {
			blockTag := "block:irrelevant"
			if tags.requestBlocked { // TODO: add block:failure to the mix
				blockTag = "block:success"
			}
			telemetry.Count(telemetry.NamespaceAppSec, "rasp.rule.match", append([]string{
				blockTag,
			}, t.baseRASPTags[ruleType]...)).Submit(1)
		}
	case waf.DefaultScope, "":
		if metric, ok := t.wafRequestsCounts[tags]; ok {
			metric.Submit(1)
		}
	default:
		telemetrylog.Error("unexpected scope name: %v", addrs.Scope, telemetry.WithTags([]string{"product:appsec"}))
	}
}

// IncWafError should be called if go-libddwaf.(*Context).Run() returns an error
func (t *Metrics) IncWafError(addrs waf.RunAddressData, in error) {
	if in == nil {
		return
	}

	if !errors.Is(in, wafErrors.ErrTimeout) {
		telemetrylog.Error("unexpected WAF error: %v", in, telemetry.WithTags(append([]string{
			"product:appsec",
		}, t.baseTags...)))
	}

	switch addrs.Scope {
	case waf.RASPScope:
		ruleType, ok := addresses.RASPRuleTypeFromAddressSet(addrs)
		if !ok {
			telemetrylog.Error("unexpected call to RASPRuleTypeFromAddressSet: %v", in, telemetry.WithTags([]string{"product:appsec"}))
		}
		t.raspError(in, ruleType)
	case waf.DefaultScope, "":
		t.wafError(in)
	default:
		telemetrylog.Error("unexpected scope name: %v", addrs.Scope, telemetry.WithTags([]string{"product:appsec"}))
	}
}

func (t *Metrics) wafError(in error) {
	t.wafErrorsSum++
	errCode := -127
	if code := wafErrors.ToWafErrorCode(in); code != 0 {
		errCode = code
	}

	telemetry.Count(telemetry.NamespaceAppSec, "waf.error", append([]string{
		"error_code:" + strconv.Itoa(errCode),
	}, t.baseTags...)).Submit(1)
}

func (t *Metrics) raspError(in error, ruleType addresses.RASPRuleType) {
	t.raspErrorsSum++
	errCode := -127
	if code := wafErrors.ToWafErrorCode(in); code != 0 {
		errCode = code
	}

	telemetry.Count(telemetry.NamespaceAppSec, "rasp.error", append([]string{
		"error_code:" + strconv.Itoa(errCode),
	}, t.baseRASPTags[ruleType]...)).Submit(1)
}
