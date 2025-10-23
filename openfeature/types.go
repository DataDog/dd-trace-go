// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import "time"

// serverConfiguration represents the top-level configuration received from Remote Config.
// It contains the feature flags configuration for a specific environment.
type serverConfiguration struct {
	FlagConfiguration universalFlagsConfiguration `json:"flag_configuration"`
}

// universalFlagsConfiguration represents the universal feature flags configuration structure.
// of the openfeature standard for server-side flag configurations.
type universalFlagsConfiguration struct {
	// CreatedAt is the timestamp when the configuration was created (RFC 3339 datetime)
	CreatedAt time.Time `json:"createdAt"`
	// Format should always be "SERVER" for server-side configurations
	Format string `json:"format"`
	// Environment contains information about the environment this configuration applies to
	Environment environment `json:"environment"`
	// Flags is a map of feature flag keys to their configurations
	Flags map[string]*flag `json:"flags"`
}

// environment represents environment information for the configuration.
type environment struct {
	// Name is the environment name (e.g., "production", "staging")
	Name string `json:"name"`
}

// valueType represents the data type of variation values for a feature flag.
type valueType string

const (
	// valueTypeBoolean represents a boolean flag type
	valueTypeBoolean valueType = "BOOLEAN"
	// valueTypeInteger represents an integer flag type
	valueTypeInteger valueType = "INTEGER"
	// valueTypeNumeric represents a numeric (float) flag type
	valueTypeNumeric valueType = "NUMERIC"
	// valueTypeString represents a string flag type
	valueTypeString valueType = "STRING"
	// valueTypeJSON represents a JSON (any structure) flag type
	valueTypeJSON valueType = "JSON"
)

// flag represents the configuration for a single feature flag.
// It contains variants and allocation rules that determine which variant
// a user receives based on targeting conditions.
type flag struct {
	// Key is the unique identifier for this feature flag
	Key string `json:"key"`
	// Enabled indicates whether the flag is active
	Enabled bool `json:"enabled"`
	// VariationType specifies the data type of variation values
	VariationType valueType `json:"variationType"`
	// Variations is a map of variant keys to their configurations
	Variations map[string]*variant `json:"variations"`
	// Allocations define targeting and traffic distribution rules
	// They are evaluated in order, and the first matching allocation wins
	Allocations []*allocation `json:"allocations"`
}

// variant represents a single variation/variation of a feature flag.
type variant struct {
	// Key uniquely identifies this variant
	Key string `json:"key"`
	// Value is the actual value for this variant
	// The type depends on the flag's VariationType:
	// - BOOLEAN: bool
	// - INTEGER: int64 or float64 (will be converted)
	// - NUMERIC: float64
	// - STRING: string
	// - JSON: any JSON-serializable value
	Value any `json:"value"`
}

// allocation defines how traffic should be allocated to variants.
// It includes targeting rules and traffic distribution (sharding) configuration.
type allocation struct {
	// Key uniquely identifies this allocation
	Key string `json:"key"`
	// Rules are targeting rules that must be satisfied for this allocation to apply
	// At least one rule must match (OR logic between rules)
	Rules []*rule `json:"rules"`
	// StartAt is the optional start time for this allocation (RFC 3339)
	StartAt *time.Time `json:"startAt,omitempty"`
	// EndAt is the optional end time for this allocation (RFC 3339)
	EndAt *time.Time `json:"endAt,omitempty"`
	// Splits define how to distribute traffic among variants
	Splits []*split `json:"splits"`
	// DoLog indicates whether to log events for this allocation (defaults to true)
	DoLog *bool `json:"doLog,omitempty"`
}

// rule represents a targeting rule containing conditions.
// All conditions within a rule must be satisfied (AND logic).
type rule struct {
	// Conditions are the individual conditions that must all be true
	Conditions []*condition `json:"conditions"`
}

// conditionOperator represents the comparison operator for a condition.
type conditionOperator string

const (
	// operatorLT checks if attribute < value (value: number)
	operatorLT conditionOperator = "LT"
	// operatorLTE checks if attribute <= value (value: number)
	operatorLTE conditionOperator = "LTE"
	// operatorGT checks if attribute > value (value: number)
	operatorGT conditionOperator = "GT"
	// operatorGTE checks if attribute >= value (value: number)
	operatorGTE conditionOperator = "GTE"

	// operatorMatches checks if attribute matches regex pattern (value: string regex)
	operatorMatches conditionOperator = "MATCHES"
	// operatorNotMatches checks if attribute doesn't match regex pattern (value: string regex)
	operatorNotMatches conditionOperator = "NOT_MATCHES"

	// operatorOneOf checks if attribute is in the list (value: []string)
	operatorOneOf conditionOperator = "ONE_OF"
	// operatorNotOneOf checks if attribute is not in the list (value: []string)
	operatorNotOneOf conditionOperator = "NOT_ONE_OF"

	// operatorIsNull checks if attribute is null/absent (value: bool)
	// If value is true, attribute must be absent; if false, attribute must be present
	operatorIsNull conditionOperator = "IS_NULL"
)

// condition represents a single condition for attribute-based targeting.
type condition struct {
	// Operator is the comparison operator to use
	Operator conditionOperator `json:"operator"`
	// Attribute is the name of the attribute to evaluate (e.g., "user_id", "country")
	Attribute string `json:"attribute"`
	// Value is the value to compare against
	// Type depends on the operator:
	// - Numeric operators (LT, LTE, GT, GTE): number (int64 or float64)
	// - Regex operators (MATCHES, NOT_MATCHES): string (regex pattern)
	// - List operators (ONE_OF, NOT_ONE_OF): []any or []string
	// - Null check (IS_NULL): bool
	Value any `json:"value"`
}

// split defines how traffic should be distributed for a specific variant.
type split struct {
	// Shards define the traffic segments this split applies to
	// All shards must match for the split to apply (AND logic)
	Shards []*shard `json:"shards"`
	// VariationKey is the key of the variation to return if this split matches
	VariationKey string `json:"variationKey"`
	// ExtraLogging contains additional metadata for logging purposes
	ExtraLogging map[string]string `json:"extraLogging,omitempty"`
}

// shard defines a portion of traffic using consistent hashing.
// It uses a salt value and hash ranges to deterministically assign users to segments.
type shard struct {
	// Salt is used in hash calculation for traffic distribution
	Salt string `json:"salt"`
	// Ranges are the hash ranges this shard covers
	Ranges []*shardRange `json:"ranges"`
	// TotalShards is the total number of possible shards (typically 8192)
	TotalShards int `json:"totalShards"`
}

// shardRange represents a range of hash values included in a shard.
type shardRange struct {
	// Start is the beginning of the range (inclusive)
	Start int `json:"start"`
	// End is the end of the range (exclusive)
	End int `json:"end"`
}
