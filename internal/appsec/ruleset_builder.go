// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package appsec

import (
	"bytes"
	"encoding/json"

	rc "github.com/DataDog/datadog-agent/pkg/remoteconfig/state"
)

type (
	// ruleset is used to build a full ruleset from a combination of ruleset fragments
	ruleset struct {
		Latest   rulesetFragment
		base     rulesetFragment
		basePath string
		edits    map[string]rulesetFragment
	}
	// rulesetFragment can represent a full ruleset or a fragment of it
	rulesetFragment struct {
		Version    json.RawMessage      `json:"version,omitempty"`
		Metadata   json.RawMessage      `json:"metadata,omitempty"`
		Rules      []ruleEntry          `json:"rules,omitempty"`
		Overrides  []rulesOverrideEntry `json:"rules_override,omitempty"`
		Exclusions []exclusionEntry     `json:"exclusions,omitempty"`
		RulesData  []ruleDataEntry      `json:"rules_data,omitempty"`
		Actions    []actionEntry        `json:"actions,omitempty"`
	}

	actionEntry struct {
		ID   string `json:"id"`
		Type string `json:"type"`
		//TODO
	}
	actionEntries []actionEntry

	ruleEntry struct {
		ID           string                     `json:"id"`
		Name         string                     `json:"name"`
		Tags         map[string]json.RawMessage `json:"tags"`
		Conditions   json.RawMessage            `json:"conditions"`
		Transformers json.RawMessage            `json:"transformers"`
	}
	ruleEntries []ruleEntry

	rulesOverrideEntry struct {
		Enabled bool   `json:"enabled"`
		ID      string `json:"id"`
	}
	rulesOverrideEntries []rulesOverrideEntry

	exclusionEntry struct {
		//TODO
		ID string `json:"id"`
	}
	exclusionEntries []exclusionEntry

	ruleDataEntry struct {
		rc.ASMDataRuleData
	}
	ruleDataEntries []ruleDataEntry
	// ASMDataRulesData is a serializable array of rules data entries
	rulesData struct {
		RulesData ruleDataEntries `json:"rules_data"`
	}

	Identifier interface {
		Ident() string
	}
)

func (e ruleEntry) Ident() string {
	return e.ID
}

func (e actionEntry) Ident() string {
	return e.ID
}

func (e rulesOverrideEntry) Ident() string {
	return e.ID
}

func (e exclusionEntry) Ident() string {
	return e.ID
}

func (e ruleDataEntry) Ident() string {
	return e.ID
}

// Default resets the ruleset to the default embedded security rules
func (r_ *rulesetFragment) Default() {
	buf := new(bytes.Buffer)
	if err := json.Compact(buf, []byte(staticRecommendedRules)); err != nil {
		return
	}
	if err := json.Unmarshal(buf.Bytes(), r_); err != nil {
		return
	}
}

func NewRuleset() *ruleset {
	var f rulesetFragment
	f.Default()
	return &ruleset{
		Latest: f,
		base:   f,
		edits:  map[string]rulesetFragment{},
	}
}

// Compile compiles the ruleset fragments together and returns the result as raw data
func (r *ruleset) Compile() rulesetFragment {
	if r.base.Rules == nil || len(r.base.Rules) == 0 {
		r.base.Default()
	}
	r.Latest = r.base

	for _, v := range r.edits {
		r.Latest = mergeRulesetFragments(r.Latest, v)
	}

	return r.Latest
}

func mergeIdentifiers[T Identifier](i1, i2 []T) []T {
	mergeMap := map[string]T{}
	res := []T{}
	for _, i := range i1 {
		mergeMap[i.Ident()] = i
	}
	for _, i := range i2 {
		mergeMap[i.Ident()] = i
	}
	for _, v := range mergeMap {
		res = append(res, v)
	}

	return res
}

func mergeRulesetFragments(f1 rulesetFragment, f2 rulesetFragment) rulesetFragment {
	merged := rulesetFragment{}
	merged.Rules = mergeIdentifiers(f1.Rules, f2.Rules)
	merged.Actions = mergeIdentifiers(f1.Actions, f2.Actions)
	merged.Overrides = mergeIdentifiers(f1.Overrides, f2.Overrides)
	merged.Exclusions = mergeIdentifiers(f1.Exclusions, f2.Exclusions)
	merged.RulesData = mergeIdentifiers(f1.RulesData, f2.RulesData)

	return merged
}
