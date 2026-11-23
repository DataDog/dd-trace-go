// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package tracer

import (
	"sync/atomic"

	"github.com/DataDog/datadog-agent/pkg/obfuscate"
)

// agentInfoObfuscation is the part of the trace-agent's /info
// config.obfuscation object that determines how the agent obfuscates the
// resources of SQL stats groups.
type agentInfoObfuscation struct {
	// SQLObfuscationMode is the agent's effective SQL obfuscation mode.
	SQLObfuscationMode obfuscate.ObfuscationMode `json:"sql_obfuscation_mode"`
	// SQL is the agent's effective SQL obfuscation config. Agents that don't
	// report it configure SQL obfuscation through feature flags and
	// SQLObfuscationMode only.
	SQL *agentInfoSQLObfuscation `json:"sql"`
}

// agentInfoSQLObfuscation mirrors the config.obfuscation.sql object of the
// trace-agent's /info response.
type agentInfoSQLObfuscation struct {
	ReplaceDigits                 bool                      `json:"replace_digits"`
	KeepSQLAlias                  bool                      `json:"keep_sql_alias"`
	DollarQuotedFunc              bool                      `json:"dollar_quoted_func"`
	KeepNull                      bool                      `json:"keep_null"`
	KeepBoolean                   bool                      `json:"keep_boolean"`
	KeepPositionalParameter       bool                      `json:"keep_positional_parameter"`
	KeepTrailingSemicolon         bool                      `json:"keep_trailing_semicolon"`
	KeepIdentifierQuotation       bool                      `json:"keep_identifier_quotation"`
	ReplaceBindParameter          bool                      `json:"replace_bind_parameter"`
	RemoveSpaceBetweenParentheses bool                      `json:"remove_space_between_parentheses"`
	KeepJSONPath                  bool                      `json:"keep_json_path"`
	ObfuscationMode               obfuscate.ObfuscationMode `json:"obfuscation_mode"`
}

// agentStatsSQLConfig returns the SQL obfuscation config the trace-agent
// applies to the stats it obfuscates itself, so that client-side stats
// obfuscation produces the same resources. af must already carry the agent's
// feature flags and obfuscation version. ok is false when the agent reports a
// config this tracer cannot reproduce; stats obfuscation must then be left to
// the agent.
func agentStatsSQLConfig(af *agentFeatures, o agentInfoObfuscation) (cfg obfuscate.SQLConfig, ok bool) {
	cfg = obfuscate.SQLConfig{
		TableNames:       af.HasFlag("table_names"),
		ReplaceDigits:    af.HasFlag("quantize_sql_tables") || af.HasFlag("replace_sql_digits"),
		KeepSQLAlias:     af.HasFlag("keep_sql_alias"),
		DollarQuotedFunc: af.HasFlag("dollar_quoted_func"),
	}
	if af.obfuscationVersion < 2 {
		// Version 1 agents report obfuscation_version 1 only for their default
		// SQL config, which the feature flags above already describe.
		return cfg, true
	}
	if s := o.SQL; s != nil {
		cfg = obfuscate.SQLConfig{
			TableNames:                    cfg.TableNames,
			ReplaceDigits:                 s.ReplaceDigits,
			KeepSQLAlias:                  s.KeepSQLAlias,
			DollarQuotedFunc:              s.DollarQuotedFunc,
			KeepNull:                      s.KeepNull,
			KeepBoolean:                   s.KeepBoolean,
			KeepPositionalParameter:       s.KeepPositionalParameter,
			KeepTrailingSemicolon:         s.KeepTrailingSemicolon,
			KeepIdentifierQuotation:       s.KeepIdentifierQuotation,
			ReplaceBindParameter:          s.ReplaceBindParameter,
			RemoveSpaceBetweenParentheses: s.RemoveSpaceBetweenParentheses,
			KeepJSONPath:                  s.KeepJSONPath,
			ObfuscationMode:               s.ObfuscationMode,
		}
	} else {
		cfg.ObfuscationMode = o.SQLObfuscationMode
	}
	switch cfg.ObfuscationMode {
	case "", obfuscate.NormalizeOnly, obfuscate.ObfuscateOnly, obfuscate.ObfuscateAndNormalize:
		return cfg, true
	default:
		return cfg, false
	}
}

// statsObfuscator hands out an obfuscator matching the agent's current SQL
// obfuscation config. The config can change when the agent restarts under a
// running tracer, so the obfuscator is rebuilt whenever a different config is
// requested.
type statsObfuscator struct {
	cur atomic.Pointer[configuredObfuscator]
}

type configuredObfuscator struct {
	cfg obfuscate.SQLConfig
	o   *obfuscate.Obfuscator
}

// forConfig returns an obfuscator that uses cfg. A nil statsObfuscator returns
// a nil obfuscator, which leaves resources unobfuscated.
func (s *statsObfuscator) forConfig(cfg obfuscate.SQLConfig) *obfuscate.Obfuscator {
	if s == nil {
		return nil
	}
	cur := s.cur.Load()
	if cur != nil && cur.cfg == cfg {
		return cur.o
	}
	// The obfuscator query cache is disabled, so a replaced obfuscator holds
	// no resources that need stopping.
	next := &configuredObfuscator{cfg: cfg, o: obfuscate.NewObfuscator(obfuscate.Config{SQL: cfg})}
	// A concurrent caller may install its own obfuscator first; each caller
	// still uses one matching the snapshot it read.
	s.cur.CompareAndSwap(cur, next)
	return next.o
}
