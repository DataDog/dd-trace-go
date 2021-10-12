// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package types

import "time"

// RawAttackMetadata is the raw attack metadata returned by the WAF when matching.
type RawAttackMetadata struct {
	Time time.Time
	// Metadata is the raw JSON representation of the AttackMetadata slice.
	Metadata []byte
}

// AttackMetadata is the parsed metadata returned by the WAF.
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
