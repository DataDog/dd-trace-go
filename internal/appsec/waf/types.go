// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package waf

import "fmt"

// RunError the WAF can return when running it.
type RunError int

// Errors the WAF can return when running it.
const (
	ErrInternal RunError = iota + 1
	ErrInvalidObject
	ErrInvalidArgument
	ErrTimeout
	ErrOutOfMemory
	ErrEmptyRuleAddresses
)

// Error returns the string representation of the RunError.
func (e RunError) Error() string {
	switch e {
	case ErrInternal:
		return "internal waf error"
	case ErrTimeout:
		return "waf timeout"
	case ErrInvalidObject:
		return "invalid waf object"
	case ErrInvalidArgument:
		return "invalid waf argument"
	case ErrOutOfMemory:
		return "out of memory"
	case ErrEmptyRuleAddresses:
		return "empty rule addresses"
	default:
		return fmt.Sprintf("unknown waf error %d", e)
	}
}

// AttackMetadata is the JSON metadata returned the WAF when it matches.
type AttackMetadata []struct {
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
