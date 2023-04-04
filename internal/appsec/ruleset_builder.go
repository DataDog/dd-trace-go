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
	// The `base` fragment is the default ruleset (either local or received through ASM_DD),
	// and the `edits` fragments each represent a remote configuration update that affects the rules.
	// `basePath` is either empty if the local base rules are used, or holds the path of the ASM_DD config.
	ruleset struct {
		Latest   rulesetFragment
		base     rulesetFragment
		basePath string
		edits    map[string]rulesetFragment
	}
	// rulesetFragment can represent a full ruleset or a fragment of it.
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
		//TODO (Francois): specify when handling user defined actions
	}

	ruleEntry struct {
		ID           string                     `json:"id"`
		Name         string                     `json:"name"`
		Tags         map[string]json.RawMessage `json:"tags"`
		Conditions   interface{}                `json:"conditions"`
		Transformers interface{}                `json:"transformers"`
	}

	rulesOverrideEntry struct {
		ID          string        `json:"id,omitempty"`
		RulesTarget []interface{} `json:"rules_target,omitempty"`
		Enabled     bool          `json:"enabled,omitempty"`
		OnMatch     interface{}   `json:"on_match,omitempty"`
	}

	exclusionEntry struct {
		ID          string        `json:"id"`
		Conditions  []interface{} `json:"conditions,omitempty"`
		Inputs      []interface{} `json:"inputs,omitempty"`
		RulesTarget []interface{} `json:"rules_target,omitempty"`
	}

	ruleDataEntry struct {
		rc.ASMDataRuleData
	}
	rulesData struct {
		RulesData []ruleDataEntry `json:"rules_data"`
	}
)

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

// validate checks that a rule override entry complies with the rule override RFC
func (o *rulesOverrideEntry) validate() bool {
	return len(o.ID) > 0 || o.RulesTarget != nil
}

// validate checks that an exclusion entry complies with the exclusion filter RFC
func (e *exclusionEntry) validate() bool {
	return len(e.Inputs) > 0 || len(e.Conditions) > 0 || len(e.RulesTarget) > 0
}

// validate checks that the ruleset fragment's fields comply with all relevant RFCs
func (r_ *rulesetFragment) validate() bool {
	for _, o := range r_.Overrides {
		if !o.validate() {
			return false
		}
	}
	for _, e := range r_.Exclusions {
		if !e.validate() {
			return false
		}
	}
	// TODO (Francois): validate more fields once we implement more RC capabilities
	return true
}

// NewRuleset initializes and returns a new ruleset using the default security rules
func NewRuleset() *ruleset {
	var f rulesetFragment
	f.Default()
	return &ruleset{
		Latest: f,
		base:   f,
		edits:  map[string]rulesetFragment{},
	}
}

// Compile compiles the ruleset fragments together and returns the compound result
func (r *ruleset) Compile() rulesetFragment {
	if r.base.Rules == nil || len(r.base.Rules) == 0 {
		r.base.Default()
	}
	r.Latest = r.base

	// Simply concatenate the content of each top level rule field as specified in our RFCs
	for _, v := range r.edits {
		r.Latest.Overrides = append(r.Latest.Overrides, v.Overrides...)
		r.Latest.Exclusions = append(r.Latest.Exclusions, v.Exclusions...)
		// TODO (Francois): process more fields once we expose the adequate capabilities (custom actions, custom rules, etc...)
	}

	return r.Latest
}
