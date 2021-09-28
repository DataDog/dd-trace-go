// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package waf

import (
	"time"

	httpinstr "gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/instrumentation/http"
	appsectypes "gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/types"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/dyngo"

	"github.com/sqreen/go-libsqreen/waf"
	"github.com/sqreen/go-libsqreen/waf/types"
)

// NewOperationEventListener returns the WAF's event listener to register in order to enable it.
func NewOperationEventListener() dyngo.EventListener {
	subscriptions := []string{
		"server.request.query",
		"server.request.headers.no_cookies",
	}
	wafRule, err := waf.NewRule(staticWAFRule)
	if err != nil {
		panic(err)
	}
	return httpinstr.OnHandlerOperationStartListener(func(op *dyngo.Operation, args httpinstr.HandlerOperationArgs) {
		// For this handler operation lifetime, create a WAF context, the set of subscribed data seen and the list of
		// detected attacks
		var (
			attacks []RawAttackMetadata
			wafCtx  = waf.NewAdditiveContext(wafRule)
			set     = types.DataSet{}
		)

		httpinstr.OnHandlerOperationFinish(op, func(op *dyngo.Operation, res httpinstr.HandlerOperationRes) {
			wafCtx.Close()
			if len(attacks) > 0 {
				op.EmitData(appsectypes.NewSecurityEvent(attacks, httpinstr.MakeHTTPOperationContext(args, res)))
			}
		})

		subscribe(op, subscriptions, wafCtx, set, &attacks)
	})
}

func subscribe(op *dyngo.Operation, subscriptions []string, wafCtx types.Rule, set types.DataSet, attacks *[]RawAttackMetadata) {
	run := func(addr string) dyngo.EventListenerFunc {
		return func(_ *dyngo.Operation, v interface{}) {
			set[addr] = v
			runWAF(wafCtx, set, attacks)
		}
	}
	var dataPtr interface{}
	for _, addr := range subscriptions {
		switch addr {
		case "http.user_agent":
			dataPtr = (*httpinstr.UserAgent)(nil)
		case "server.request.headers.no_cookies":
			dataPtr = (*httpinstr.Headers)(nil)
		case "server.request.query":
			dataPtr = (*httpinstr.QueryValues)(nil)
		default:
			continue
		}
		op.OnData(dataPtr, run(addr))
	}
}

type (
	// RawAttackMetadata is the raw attack metadata returned by the WAF when matching.
	RawAttackMetadata struct {
		Time time.Time
		// Block states if the operation where this event happened should be blocked.
		Block bool
		// Metadata is the raw JSON representation of the AttackMetadata slice.
		Metadata []byte
	}

	// AttackMetadata is the parsed metadata returned by the WAF.
	AttackMetadata []struct {
		RetCode int    `json:"ret_code"`
		Flow    string `json:"flow"`
		Step    string `json:"step"`
		Rule    string `json:"rule"`
		Filter  []struct {
			Operator        string        `json:"operator"`
			OperatorValue   string        `json:"operator_value"`
			BindingAccessor string        `json:"binding_accessor"`
			ManifestKey     string        `json:"manifest_key"`
			KeyPath         []interface{} `json:"key_path"`
			ResolvedValue   string        `json:"resolved_value"`
			MatchStatus     string        `json:"match_status"`
		} `json:"filter"`
	}
)

func runWAF(wafCtx types.Rule, set types.DataSet, attacks *[]RawAttackMetadata) {
	action, md, err := wafCtx.Run(set, 5*time.Millisecond)
	if err != nil {
		panic(err)
	}
	if action == types.NoAction {
		return
	}
	*attacks = append(*attacks, RawAttackMetadata{Time: time.Now(), Block: action == types.BlockAction, Metadata: md})
}
