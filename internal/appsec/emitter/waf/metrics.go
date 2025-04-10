// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package waf

import (
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/waf/addresses"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	telemetrylog "github.com/DataDog/dd-trace-go/v2/internal/telemetry/log"
	"github.com/DataDog/go-libddwaf/v4"
	"github.com/DataDog/go-libddwaf/v4/waferrors"
	"github.com/puzpuzpuz/xsync/v3"
)

// newHandleTelemetryMetric is the name of the metric that will be used to track the initialization of the WAF handle
// this values is changed to waf.updates after the first call to [NewMetricsInstance]
var newHandleTelemetryMetric = "waf.init"
var changeToWafUpdates sync.Once

// RequestMilestones is a list of things that can happen as a result of a waf call. They are stacked for each requests
// and used as tags to the telemetry metric `waf.requests`.
// this struct can be modified concurrently.
// TODO: add request_excluded and block_failure to the mix once we have the capability to track them
type RequestMilestones struct {
	requestBlocked bool
	ruleTriggered  bool
	wafTimeout     bool
	rateLimited    bool
	wafError       bool
}

// HandleMetrics is a struct that holds all the telemetry metrics for the WAF that lives and die alongside with the WAF handle
type HandleMetrics struct {
	baseTags     []string
	baseRASPTags map[addresses.RASPRuleType][]string

	// truncationCounts holds the telemetry metrics for the `waf.input_truncated` metric
	truncationCounts map[libddwaf.TruncationReason]telemetry.MetricHandle
	// truncationDistributions holds the telemetry metrics for the `waf.truncated_value_size` metric
	truncationDistributions map[libddwaf.TruncationReason]telemetry.MetricHandle
	// wafTimerDistributions holds the telemetry metrics for the `rasp.timeout`, `rasp.duration`, `rasp.duration_ext`, `waf.duration`, `waf.duration_ext` metrics
	wafTimerDistributions map[string]telemetry.MetricHandle
	// raspTimeoutCount holds the telemetry metrics for the rasp.timeout metrics since there is not waf.timeout metric
	raspTimeoutCount telemetry.MetricHandle
	// raspRuleEval holds the telemetry metrics for the `rasp.rule_eval` metric by rule type
	raspRuleEval map[addresses.RASPRuleType]telemetry.MetricHandle

	// wafRequestsCounts holds the telemetry metrics for the `waf.requests` metric, this one is lazily filled by the [ContextMetrics]
	wafRequestsCounts *xsync.MapOf[RequestMilestones, telemetry.MetricHandle]
}

var baseRASPTags = map[addresses.RASPRuleType][]string{
	addresses.RASPRuleTypeLFI:  {"rule_type:" + string(addresses.RASPRuleTypeLFI)},
	addresses.RASPRuleTypeSSRF: {"rule_type:" + string(addresses.RASPRuleTypeSSRF)},
	addresses.RASPRuleTypeSQLI: {"rule_type:" + string(addresses.RASPRuleTypeSQLI)},
	addresses.RASPRuleTypeCMDI: {"rule_type:" + string(addresses.RASPRuleTypeCMDI), "rule_variant:exec"},
}

// NewMetricsInstance creates a new HandleMetrics struct and submit the `waf.init` or `waf.updates` metric. To be called with the raw results of the WAF handle initialization
func NewMetricsInstance(newHandle *libddwaf.Handle, eventRulesVersion string) HandleMetrics {
	telemetry.Count(telemetry.NamespaceAppSec, newHandleTelemetryMetric, []string{
		"waf_version:" + libddwaf.Version(),
		"event_rules_version:" + eventRulesVersion,
		"success:" + strconv.FormatBool(newHandle != nil),
	}).Submit(1)

	changeToWafUpdates.Do(func() {
		newHandleTelemetryMetric = "waf.updates"
	})

	baseTags := []string{
		"event_rules_version:" + eventRulesVersion,
		"waf_version:" + libddwaf.Version(),
	}

	raspTags := make(map[addresses.RASPRuleType][]string, len(addresses.RASPRuleTypes()))
	for _, ruleType := range addresses.RASPRuleTypes() {
		tags := make([]string, len(baseRASPTags[ruleType])+len(baseTags))
		copy(tags, baseRASPTags[ruleType])
		copy(tags[len(baseRASPTags[ruleType]):], baseTags)
		raspTags[ruleType] = tags
	}

	raspRuleEval := make(map[addresses.RASPRuleType]telemetry.MetricHandle, len(addresses.RASPRuleTypes()))
	for _, ruleType := range addresses.RASPRuleTypes() {
		raspRuleEval[ruleType] = telemetry.Count(telemetry.NamespaceAppSec, "rasp.rule.eval", raspTags[ruleType])
	}

	return HandleMetrics{
		baseTags:     baseTags,
		baseRASPTags: raspTags,
		truncationCounts: map[libddwaf.TruncationReason]telemetry.MetricHandle{
			libddwaf.StringTooLong:     telemetry.Count(telemetry.NamespaceAppSec, "waf.input_truncated", []string{"truncation_reason:" + strconv.Itoa(int(libddwaf.StringTooLong))}),
			libddwaf.ContainerTooLarge: telemetry.Count(telemetry.NamespaceAppSec, "waf.input_truncated", []string{"truncation_reason:" + strconv.Itoa(int(libddwaf.ContainerTooLarge))}),
			libddwaf.ObjectTooDeep:     telemetry.Count(telemetry.NamespaceAppSec, "waf.input_truncated", []string{"truncation_reason:" + strconv.Itoa(int(libddwaf.ObjectTooDeep))}),
		},
		truncationDistributions: map[libddwaf.TruncationReason]telemetry.MetricHandle{
			libddwaf.StringTooLong:     telemetry.Distribution(telemetry.NamespaceAppSec, "waf.truncated_value_size", []string{"truncation_reason:" + strconv.Itoa(int(libddwaf.StringTooLong))}),
			libddwaf.ContainerTooLarge: telemetry.Distribution(telemetry.NamespaceAppSec, "waf.truncated_value_size", []string{"truncation_reason:" + strconv.Itoa(int(libddwaf.ContainerTooLarge))}),
			libddwaf.ObjectTooDeep:     telemetry.Distribution(telemetry.NamespaceAppSec, "waf.truncated_value_size", []string{"truncation_reason:" + strconv.Itoa(int(libddwaf.ObjectTooDeep))}),
		},
		wafTimerDistributions: map[string]telemetry.MetricHandle{
			"rasp.duration":     telemetry.Distribution(telemetry.NamespaceAppSec, "rasp.duration", baseTags),
			"rasp.duration_ext": telemetry.Distribution(telemetry.NamespaceAppSec, "rasp.duration_ext", baseTags),
			"waf.duration":      telemetry.Distribution(telemetry.NamespaceAppSec, "waf.duration", baseTags),
			"waf.duration_ext":  telemetry.Distribution(telemetry.NamespaceAppSec, "waf.duration_ext", baseTags),
		},
		raspTimeoutCount:  telemetry.Count(telemetry.NamespaceAppSec, "rasp.timeout", baseTags),
		wafRequestsCounts: xsync.NewMapOf[RequestMilestones, telemetry.MetricHandle](xsync.WithGrowOnly(), xsync.WithPresize(2^5)),
		raspRuleEval:      raspRuleEval,
	}
}

func (m *HandleMetrics) NewContextMetrics() *ContextMetrics {
	return &ContextMetrics{
		HandleMetrics: m,
	}
}

type ContextMetrics struct {
	*HandleMetrics

	// SumRASPCalls is the sum of all the RASP calls made by the WAF whatever the rasp rule type it is.
	SumRASPCalls atomic.Uint32
	// SumWAFErrors is the sum of all the WAF errors that happened not in the RASP scope.
	SumWAFErrors atomic.Uint32
	// SumRASPErrors is the sum of all the RASP errors that happened in the RASP scope.
	SumRASPErrors atomic.Uint32

	// Milestones are the tags of the metric `waf.requests` that will be submitted at the end of the waf context
	Milestones RequestMilestones
}

// RegisterStats increment the metrics for the WAF run stats at the end of each waf context lifecycle
// It registers the metrics:
// - `rasp.duration` and `rasp.duration_ext` for the RASP scope using [libddwaf.Stats.Timers]
// - `waf.duration` and `waf.duration_ext` for the WAF scope using [libddwaf.Stats.Timers]
// - `rasp.timeout` for the RASP scope using [libddwaf.Stats.TimeoutRASPCount]
// - `waf.input_truncated` and `waf.truncated_value_size` for the truncations using [libddwaf.Stats.Truncations]
// - `waf.requests` for the milestones using [ContextMetrics.Milestones]
func (m *ContextMetrics) RegisterStats(stats libddwaf.Stats) {
	// Add metrics `{waf,rasp}.duration[_ext]`
	for key, value := range stats.Timers {
		metric, found := m.wafTimerDistributions[key]
		if !found {
			continue
		}

		// The metrics should be in microseconds
		metric.Submit(float64(value.Nanoseconds()) / float64(time.Microsecond.Nanoseconds()))
	}

	if stats.TimeoutRASPCount > 0 {
		m.raspTimeoutCount.Submit(float64(stats.TimeoutRASPCount))
	}

	// If truncations during encoding happened, increment the `waf.input_truncated` and `waf.truncated_value_size` metrics
	for reason, sizes := range stats.Truncations {
		countMetric, countFound := m.truncationCounts[reason]
		distMetric, distFound := m.truncationDistributions[reason]
		if !countFound || !distFound {
			telemetrylog.Error("unexpected truncation reason: %v", reason, telemetry.WithTags([]string{"product:appsec"}))
			continue
		}
		countMetric.Submit(1)
		for _, size := range sizes {
			distMetric.Submit(float64(size))
		}
	}

	m.incWafRequestsCounts()
}

// incWafRequestsCounts increments the `waf.requests` metric with the current milestones and creates a new metric handle if it does not exist
func (m *ContextMetrics) incWafRequestsCounts() {
	handle, _ := m.wafRequestsCounts.LoadOrCompute(m.Milestones, func() telemetry.MetricHandle {
		return telemetry.Count(telemetry.NamespaceAppSec, "waf.requests", append([]string{
			"request_blocked:" + strconv.FormatBool(m.Milestones.requestBlocked),
			"rule_triggered:" + strconv.FormatBool(m.Milestones.ruleTriggered),
			"waf_timeout:" + strconv.FormatBool(m.Milestones.wafTimeout),
			"rate_limited:" + strconv.FormatBool(m.Milestones.rateLimited),
			"waf_error:" + strconv.FormatBool(m.Milestones.wafError),
		}, m.baseTags...))
	})

	handle.Submit(1)
}

// RegisterWafRun register the different outputs of the WAF for the `waf.requests` and also directly increment the `rasp.rule.match` and `rasp.rule.eval` metrics.
// It registers the metrics:
// - `rasp.rule.match`
// - `rasp.rule.eval`
// - accumulate data to set `waf.requests` by the end of the waf context
func (m *ContextMetrics) RegisterWafRun(addrs libddwaf.RunAddressData, tags RequestMilestones) {
	switch addrs.Scope {
	case libddwaf.RASPScope:
		m.SumRASPCalls.Add(1)
		ruleType, ok := addresses.RASPRuleTypeFromAddressSet(addrs)
		if !ok {
			telemetrylog.Error("unexpected call to RASPRuleTypeFromAddressSet", telemetry.WithTags([]string{"product:appsec"}))
			return
		}
		if metric, ok := m.raspRuleEval[ruleType]; ok {
			metric.Submit(1)
		}
		if tags.ruleTriggered {
			blockTag := "block:irrelevant"
			if tags.requestBlocked { // TODO: add block:failure to the mix
				blockTag = "block:success"
			}
			telemetry.Count(telemetry.NamespaceAppSec, "rasp.rule.match", append([]string{
				blockTag,
			}, m.baseRASPTags[ruleType]...)).Submit(1)
		}
	case libddwaf.DefaultScope, "":
		if tags.requestBlocked {
			m.Milestones.requestBlocked = true
		}
		if tags.ruleTriggered {
			m.Milestones.ruleTriggered = true
		}
		if tags.wafTimeout {
			m.Milestones.wafTimeout = true
		}
		if tags.rateLimited {
			m.Milestones.rateLimited = true
		}
		if tags.wafError {
			m.Milestones.wafError = true
		}
	default:
		telemetrylog.Error("unexpected scope name: %v", addrs.Scope, telemetry.WithTags([]string{"product:appsec"}))
	}
}

// IncWafError should be called if go-libddwaf.(*Context).Run() returns an error to increments metrics linked to WAF errors
// It registers the metrics:
// - `waf.error`
// - `rasp.error`
func (m *ContextMetrics) IncWafError(addrs libddwaf.RunAddressData, in error) {
	if in == nil {
		return
	}

	if !errors.Is(in, waferrors.ErrTimeout) {
		telemetrylog.Error("unexpected WAF error: %v", in, telemetry.WithTags(append([]string{
			"product:appsec",
		}, m.baseTags...)))
	}

	switch addrs.Scope {
	case libddwaf.RASPScope:
		ruleType, ok := addresses.RASPRuleTypeFromAddressSet(addrs)
		if !ok {
			telemetrylog.Error("unexpected call to RASPRuleTypeFromAddressSet: %v", in, telemetry.WithTags([]string{"product:appsec"}))
		}
		m.raspError(in, ruleType)
	case libddwaf.DefaultScope, "":
		m.wafError(in)
	default:
		telemetrylog.Error("unexpected scope name: %v", addrs.Scope, telemetry.WithTags([]string{"product:appsec"}))
	}
}

// defaultWafErrorCode is the default error code if the error does not implement [libddwaf.RunError]
// meaning if the error actual come for the bindings and not from the WAF itself
const defaultWafErrorCode = -127

func (m *ContextMetrics) wafError(in error) {
	m.SumWAFErrors.Add(1)
	errCode := defaultWafErrorCode
	if code := waferrors.ToWafErrorCode(in); code != 0 {
		errCode = code
	}

	telemetry.Count(telemetry.NamespaceAppSec, "waf.error", append([]string{
		"error_code:" + strconv.Itoa(errCode),
	}, m.baseTags...)).Submit(1)
}

func (m *ContextMetrics) raspError(in error, ruleType addresses.RASPRuleType) {
	m.SumRASPErrors.Add(1)
	errCode := defaultWafErrorCode
	if code := waferrors.ToWafErrorCode(in); code != 0 {
		errCode = code
	}

	telemetry.Count(telemetry.NamespaceAppSec, "rasp.error", append([]string{
		"error_code:" + strconv.Itoa(errCode),
	}, m.baseRASPTags[ruleType]...)).Submit(1)
}
