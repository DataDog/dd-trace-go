// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sharedsec

import (
	"encoding/json"
	"errors"

	"gopkg.in/DataDog/dd-trace-go.v1/appsec/events"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/sharedsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/trace"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"

	"github.com/DataDog/appsec-internal-go/limiter"
	waf "github.com/DataDog/go-libddwaf/v3"
	wafErrors "github.com/DataDog/go-libddwaf/v3/errors"
)

const (
	eventRulesVersionTag = "_dd.appsec.event_rules.version"
	eventRulesErrorsTag  = "_dd.appsec.event_rules.errors"
	eventRulesLoadedTag  = "_dd.appsec.event_rules.loaded"
	eventRulesFailedTag  = "_dd.appsec.event_rules.error_count"
	wafVersionTag        = "_dd.appsec.waf.version"
)

func RunWAF(wafCtx *waf.Context, values waf.RunAddressData) waf.Result {
	result, err := wafCtx.Run(values)
	if errors.Is(err, wafErrors.ErrTimeout) {
		log.Debug("appsec: waf timeout value reached: %v", err)
	} else if err != nil {
		log.Error("appsec: unexpected waf error: %v", err)
	}
	return result
}

func MakeWAFRunListener[O dyngo.Operation, T dyngo.ArgOf[O]](
	events *trace.SecurityEventsHolder,
	wafCtx *waf.Context,
	limiter limiter.Limiter,
	toRunAddressData func(T) waf.RunAddressData,
) func(O, T) {
	return func(op O, args T) {
		wafResult := RunWAF(wafCtx, toRunAddressData(args))
		if !wafResult.HasEvents() {
			return
		}

		log.Debug("appsec: WAF detected a suspicious WAF event")

		ProcessActions(op, wafResult.Actions)
		AddSecurityEvents(events, limiter, wafResult.Events)
	}
}

// AddSecurityEvents is a helper function to add sec events to an operation taking into account the rate limiter.
func AddSecurityEvents(holder *trace.SecurityEventsHolder, limiter limiter.Limiter, matches []any) {
	if len(matches) > 0 && limiter.Allow() {
		holder.AddSecurityEvents(matches)
	}
}

// AddRulesMonitoringTags adds the tags related to security rules monitoring
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

// AddWAFMonitoringTags adds the tags related to the monitoring of the WAF
func AddWAFMonitoringTags(th trace.TagSetter, rulesVersion string, stats map[string]any) {
	// Rules version is set for every request to help the backend associate WAF duration metrics with rule version
	th.SetTag(eventRulesVersionTag, rulesVersion)

	// Report the stats sent by the WAF
	for k, v := range stats {
		th.SetTag(k, v)
	}
}

// ProcessActions sends the relevant actions to the operation's data listener.
// It returns true if at least one of those actions require interrupting the request handler
// When SDKError is not nil, this error is sent to the op with EmitData so that the invoked SDK can return it
func ProcessActions(op dyngo.Operation, actions map[string]any) (interrupt bool) {
	for aType, params := range actions {
		log.Debug("appsec: processing %s action with params %v", aType, params)
		actionArray := ActionsFromEntry(aType, params)
		if actionArray == nil {
			log.Debug("cannot process %s action with params %v", aType, params)
			continue
		}
		for _, a := range actionArray {
			a.EmitData(op)
			interrupt = interrupt || a.Blocking()
		}
	}

	// If any of the actions are supposed to interrupt the request, emit a blocking event for the SDK operations
	// to return an error.
	if interrupt {
		dyngo.EmitData(op, &events.BlockingSecurityEvent{})
	}

	return interrupt
}

// ActionsFromEntry returns one or several actions generated from the WAF returned action entry
// Several actions are returned when the action is of block_request type since we could be blocking HTTP or GRPC
func ActionsFromEntry(actionType string, params any) []sharedsec.Action {
	p, ok := params.(map[string]any)
	if !ok {
		return nil
	}
	switch actionType {
	case "block_request":
		return sharedsec.NewBlockAction(p)
	case "redirect_request":
		return []sharedsec.Action{sharedsec.NewRedirectAction(p)}
	case "generate_stack":
		return []sharedsec.Action{sharedsec.NewStackTraceAction(p)}

	default:
		log.Debug("appsec: unknown action type `%s`", actionType)
		return nil
	}
}
