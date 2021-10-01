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
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"

	"github.com/sqreen/go-libsqreen/waf"
	"github.com/sqreen/go-libsqreen/waf/types"
)

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

// List of rule addresses currently supported by the WAF
const (
	serverRequestRawURIAddr  = "server.request.uri.raw"
	serverRequestHeadersAddr = "server.request.headers.no_cookies"
)

// NewOperationEventListener returns the WAF event listener to register in order to enable it.
func NewOperationEventListener() dyngo.EventListener {
	wafRule, err := waf.NewRule(staticWAFRule)
	if err != nil {
		log.Error("appsec: waf error: %v", err)
		return nil
	}

	return httpinstr.OnHandlerOperationStartListener(func(op *dyngo.Operation, args httpinstr.HandlerOperationArgs) {
		// For this handler operation lifetime, create a WAF context and the list of detected attacks
		var (
			// TODO(julio): make the attack slice thread-safe as soon as we listen for sub-operations
			attacks []RawAttackMetadata
			wafCtx  = waf.NewAdditiveContext(wafRule)
		)

		httpinstr.OnHandlerOperationFinish(op, func(op *dyngo.Operation, res httpinstr.HandlerOperationRes) {
			wafCtx.Close()
			if len(attacks) > 0 {
				op.EmitData(appsectypes.NewSecurityEvent(attacks, httpinstr.MakeHTTPOperationContext(args, res)))
			}
		})

		// Run the WAF on the rule addresses available in the request args
		// TODO(julio): dynamically get the required addresses from the WAF rule
		headers := args.Headers.Clone()
		headers.Del("Cookie")
		values := map[string]interface{}{
			serverRequestRawURIAddr:  args.RequestURI,
			serverRequestHeadersAddr: headers,
		}
		runWAF(wafCtx, values, &attacks)
	})
}

func runWAF(wafCtx types.Rule, values types.DataSet, attacks *[]RawAttackMetadata) {
	action, md, err := wafCtx.Run(values, 1*time.Millisecond)
	if err != nil {
		log.Error("appsec: waf error: %v", err)
		return
	}
	if action == types.NoAction {
		return
	}
	*attacks = append(*attacks, RawAttackMetadata{Time: time.Now(), Block: action == types.BlockAction, Metadata: md})
}
