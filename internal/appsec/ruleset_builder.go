// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package appsec

import (
	"encoding/json"
	"fmt"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"

	rc "github.com/DataDog/datadog-agent/pkg/remoteconfig/state"
)

type (
	// rulesManager is used to build a full rules file from a combination of rules fragments
	// The `base` fragment is the default rules (either local or received through ASM_DD),
	// and the `edits` fragments each represent a remote configuration update that affects the rules.
	// `basePath` is either empty if the local base rules are used, or holds the path of the ASM_DD config.
	rulesManager struct {
		latest   rulesFragment
		base     rulesFragment
		basePath string
		edits    map[string]rulesFragment
	}
	// rulesFragment can represent a full ruleset or a fragment of it.
	rulesFragment struct {
		Version    string               `json:"version,omitempty"`
		Metadata   interface{}          `json:"metadata,omitempty"`
		Rules      []ruleEntry          `json:"rules,omitempty"`
		Overrides  []rulesOverrideEntry `json:"rules_override,omitempty"`
		Exclusions []exclusionEntry     `json:"exclusions,omitempty"`
		RulesData  []ruleDataEntry      `json:"rules_data,omitempty"`
		Actions    []interface{}        `json:"actions,omitempty"`
	}

	ruleEntry struct {
		ID           string        `json:"id"`
		Name         interface{}   `json:"name,omitempty"`
		Tags         interface{}   `json:"tags"`
		Conditions   interface{}   `json:"conditions"`
		Transformers interface{}   `json:"transformers"`
		OnMatch      []interface{} `json:"on_match,omitempty"`
	}

	rulesOverrideEntry struct {
		ID          string        `json:"id,omitempty"`
		RulesTarget []interface{} `json:"rules_target,omitempty"`
		Enabled     interface{}   `json:"enabled,omitempty"`
		OnMatch     interface{}   `json:"on_match,omitempty"`
	}

	exclusionEntry struct {
		ID          string        `json:"id"`
		Conditions  []interface{} `json:"conditions,omitempty"`
		Inputs      []interface{} `json:"inputs,omitempty"`
		RulesTarget []interface{} `json:"rules_target,omitempty"`
	}

	ruleDataEntry rc.ASMDataRuleData
	rulesData     struct {
		RulesData []ruleDataEntry `json:"rules_data"`
	}
)

// defaultRulesFragment returns a rulesFragment created using the default static recommended rules
func defaultRulesFragment() rulesFragment {
	var f rulesFragment
	if err := json.Unmarshal([]byte(staticRecommendedRules), &f); err != nil {
		log.Debug("appsec: error unmarshalling default rules: %v", err)
	}
	return f
}

// validate checks that a rule override entry complies with the rule override RFC
func (o *rulesOverrideEntry) validate() bool {
	return len(o.ID) > 0 || o.RulesTarget != nil
}

// validate checks that an exclusion entry complies with the exclusion filter RFC
func (e *exclusionEntry) validate() bool {
	return len(e.Inputs) > 0 || len(e.Conditions) > 0 || len(e.RulesTarget) > 0
}

// validate checks that the rules fragment's fields comply with all relevant RFCs
func (r_ *rulesFragment) validate() bool {
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

func (r_ *rulesFragment) clone() rulesFragment {
	var f rulesFragment
	f.Version = r_.Version
	f.Metadata = r_.Metadata
	f.Overrides = append(f.Overrides, r_.Overrides...)
	f.Exclusions = append(f.Exclusions, r_.Exclusions...)
	f.RulesData = append(f.RulesData, r_.RulesData...)
	// TODO (Francois Mazeau): copy more fields once we handle them
	return f
}

// newRulesManager initializes and returns a new rulesManager using the provided rules.
// If no rules are provided (nil), the default rules are used instead.
// If the provided rules are invalid, an error is returned
func newRulesManager(rules []byte) (*rulesManager, error) {
	var f rulesFragment
	if rules == nil {
		f = defaultRulesFragment()
		log.Debug("appsec: rulesManager: using default rules configuration")
	} else if err := json.Unmarshal(rules, &f); err != nil {
		log.Debug("appsec: cannot create rulesManager from specified rules")
		return nil, err
	}
	return &rulesManager{
		latest: f,
		base:   f,
		edits:  map[string]rulesFragment{},
	}, nil
}

func (r *rulesManager) clone() *rulesManager {
	var clone rulesManager
	clone.edits = make(map[string]rulesFragment, len(r.edits))
	for k, v := range r.edits {
		clone.edits[k] = v
	}
	clone.base = r.base.clone()
	clone.latest = r.latest.clone()
	return &clone
}

func (r *rulesManager) addEdit(cfgPath string, f rulesFragment) {
	r.edits[cfgPath] = f
}

func (r *rulesManager) removeEdit(cfgPath string) {
	delete(r.edits, cfgPath)
}

func (r *rulesManager) changeBase(f rulesFragment, basePath string) {
	r.base = f
	r.basePath = basePath
}

// compile compiles the rulesManager fragments together stores the result in r.latest
func (r *rulesManager) compile() {
	if r.base.Rules == nil || len(r.base.Rules) == 0 {
		r.base = defaultRulesFragment()
	}
	r.latest = r.base

	// Simply concatenate the content of each top level rule field as specified in our RFCs
	for _, v := range r.edits {
		r.latest.Overrides = append(r.latest.Overrides, v.Overrides...)
		r.latest.Exclusions = append(r.latest.Exclusions, v.Exclusions...)
		r.latest.Actions = append(r.latest.Actions, v.Actions...)
		r.latest.RulesData = append(r.latest.RulesData, v.RulesData...)
		// TODO (Francois): process more fields once we expose the adequate capabilities (custom actions, custom rules, etc...)
	}
}

// raw returns a compact json version of the rules
func (r *rulesManager) raw() []byte {
	data, _ := json.Marshal(r.latest)
	return data
}

// String returns the string representation of the latest compiled json rules.
func (r *rulesManager) String() string {
	return fmt.Sprintf("%+v", r.latest)
}
