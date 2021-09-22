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

func NewOperationEventListener() dyngo.EventListener {
	subscriptions := []string{
		"server.request.query",
		"server.request.headers.no_cookies",
	}
	wafRule, err := waf.NewRule(staticWAFRule)
	if err != nil {
		panic(err)
	}
	return dyngo.OnStartEventListener(func(op *dyngo.Operation, args httpinstr.HandlerOperationArgs) {
		// For this handler operation lifetime, create a WAF context, the set of subscribed data seen and the list of
		// detected attacks
		var (
			attacks []RawAttackMetadata
			wafCtx  = waf.NewAdditiveContext(wafRule)
			set     = types.DataSet{}
		)

		op.OnFinish(func(op *dyngo.Operation, res httpinstr.HandlerOperationRes) {
			wafCtx.Close()
			op.EmitData(appsectypes.NewSecurityEvent(attacks, appsectypes.WithHTTPOperationContext(args, res)))
		})

		subscribe(op, subscriptions, wafCtx, set, &attacks)
	})
}

func subscribe(op *dyngo.Operation, subscriptions []string, wafCtx types.Rule, set types.DataSet, attacks *[]RawAttackMetadata) {
	run := func(addr string, v interface{}) {
		set[addr] = v
		runWAF(wafCtx, set, attacks)
	}
	for _, addr := range subscriptions {
		addr := addr
		switch addr {
		case "http.user_agent":
			op.OnData(func(data httpinstr.UserAgent) {
				run(addr, data)
			})
		case "server.request.headers.no_cookies":
			op.OnData(func(data httpinstr.Header) {
				run(addr, data)
			})
		case "server.request.query":
			op.OnData(func(data httpinstr.QueryValues) {
				run(addr, data)
			})
		}
	}
}

type (
	RawAttackMetadata struct {
		Time     time.Time
		Block    bool
		Metadata []byte
	}

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
