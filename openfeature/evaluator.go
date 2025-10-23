// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"crypto/md5"
	"encoding/binary"
	"fmt"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	of "github.com/open-feature/go-sdk/openfeature"
)

// evaluationResult contains the result of a flag evaluation.
type evaluationResult struct {
	// Value is the evaluated variant value
	Value any
	// VariantKey is the key of the selected variant
	VariantKey string
	// Reason describes why this value was returned
	Reason of.Reason
	// Error contains any error that occurred during evaluation
	Error error
}

// evaluateFlag evaluates a feature flag with the given context.
// It returns the variant value, reason, and any error that occurred.
func evaluateFlag(flag *flag, defaultValue any, context map[string]any) evaluationResult {
	// Check if flag is enabled
	if !flag.Enabled {
		return evaluationResult{
			Value:  defaultValue,
			Reason: of.DisabledReason,
		}
	}

	// Evaluate allocations in order - first match wins
	now := time.Now()
	for _, allocation := range flag.Allocations {
		split, matched := evaluateAllocation(allocation, context, now)
		if matched && split != nil {
			// Find the variant for this split
			variant, ok := flag.Variations[split.VariationKey]
			if !ok {
				return evaluationResult{
					Value:  defaultValue,
					Reason: of.ErrorReason,
					Error:  fmt.Errorf("variation key %q not found in flag variations", split.VariationKey),
				}
			}

			// Validate variant type matches flag type
			if err := validateVariantType(variant.Value, flag.VariationType); err != nil {
				return evaluationResult{
					Value:  defaultValue,
					Reason: of.ErrorReason,
					Error:  fmt.Errorf("variant type mismatch: %w", err),
				}
			}

			return evaluationResult{
				Value:      variant.Value,
				VariantKey: variant.Key,
				Reason:     of.TargetingMatchReason,
			}
		}
	}

	// No allocations matched, return default
	return evaluationResult{
		Value:  defaultValue,
		Reason: of.DefaultReason,
	}
}

// evaluateAllocation evaluates an allocation and returns the matching split if any.
func evaluateAllocation(allocation *allocation, context map[string]any, currentTime time.Time) (*split, bool) {
	// Check time window constraints
	if allocation.StartAt != nil && currentTime.Before(*allocation.StartAt) {
		return nil, false
	}
	if allocation.EndAt != nil && currentTime.After(*allocation.EndAt) {
		return nil, false
	}

	// Check if any rule matches (OR logic between rules)
	// If there are no rules, the allocation matches everyone
	ruleMatched := len(allocation.Rules) == 0
	for _, rule := range allocation.Rules {
		if evaluateRule(rule, context) {
			ruleMatched = true
			break
		}
	}

	if !ruleMatched {
		return nil, false
	}

	// Evaluate splits to determine which variant
	for _, split := range allocation.Splits {
		if evaluateSplit(split, context) {
			return split, true
		}
	}

	return nil, false
}

// evaluateRule evaluates a rule by checking all conditions (AND logic).
func evaluateRule(rule *rule, context map[string]any) bool {
	for _, condition := range rule.Conditions {
		if !evaluateCondition(condition, context) {
			return false
		}
	}
	return true
}

// evaluateCondition evaluates a single condition against the context.
func evaluateCondition(condition *condition, context map[string]any) bool {
	attributeValue, exists := context[condition.Attribute]

	// Special handling for "id" attribute: if not explicitly provided, use targeting key
	if condition.Attribute == "id" && !exists {
		if targetingKey, ok := context[of.TargetingKey].(string); ok {
			attributeValue = targetingKey
			exists = true
		}
	}

	// Special handling for IS_NULL operator
	if condition.Operator == operatorIsNull {
		isNull := !exists || attributeValue == nil
		expectedNull, ok := condition.Value.(bool)
		if !ok {
			return false
		}
		return isNull == expectedNull
	}

	// For all other operators, attribute must exist
	if !exists || attributeValue == nil {
		return false
	}

	switch condition.Operator {
	case operatorMatches:
		return matchesRegex(attributeValue, condition.Value)
	case operatorNotMatches:
		return !matchesRegex(attributeValue, condition.Value)
	case operatorOneOf:
		return isOneOf(attributeValue, condition.Value)
	case operatorNotOneOf:
		return !isOneOf(attributeValue, condition.Value)
	case operatorGT, operatorGTE, operatorLT, operatorLTE:
		return evaluateNumericCondition(attributeValue, condition.Value, condition.Operator)
	default:
		return false
	}
}

var regexCache sync.Map // map[string]*regexp.Regexp

// loadRegex loads or compiles a regex pattern with caching.
// Returns the compiled regex or an error if compilation fails.
func loadRegex(pattern string) (*regexp.Regexp, error) {
	// First, check if it's already compiled
	if regexAny, ok := regexCache.Load(pattern); ok {
		if regex, ok := regexAny.(*regexp.Regexp); ok {
			return regex, nil
		}
	}

	// Not in cache, compile it (we are probably in the remote config goroutine, so this is acceptable)
	compiled, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}

	regexCache.Store(pattern, compiled)
	return compiled, nil
}

// matchesRegex checks if the attribute value matches the regex pattern.
func matchesRegex(attributeValue any, conditionValue any) bool {
	pattern, ok := conditionValue.(string)
	if !ok {
		return false
	}

	regex, err := loadRegex(pattern)
	if err != nil {
		log.Debug("openfeature: failed to compile regex pattern %q: %v", pattern, err.Error())
		return false
	}

	var valueStr string
	switch v := attributeValue.(type) {
	case string:
		valueStr = v
	case int, int8, int16, int32, int64:
		if i64, err := internal.ToInt64(v); err == nil {
			valueStr = strconv.FormatInt(i64, 10)
		} else {
			return false
		}
	case uint, uint8, uint16, uint32, uint64:
		if u64, err := internal.ToUint64(v); err == nil {
			valueStr = strconv.FormatUint(u64, 10)
		} else {
			return false
		}
	case float32, float64:
		if f64, ok := internal.ToFloat64(v); ok {
			valueStr = strconv.FormatFloat(f64, 'f', -1, 64)
		} else {
			return false
		}
	case bool:
		valueStr = strconv.FormatBool(v)
	default:
		return false
	}

	return regex.MatchString(valueStr)
}

// isOneOf checks if the attribute value is in the list of condition values.
func isOneOf(attributeValue any, conditionValue any) bool {
	// Convert condition value to string slice
	var conditionList []string
	switch cv := conditionValue.(type) {
	case []string:
		conditionList = cv
	case []any:
		conditionList = make([]string, len(cv))
		for i, v := range cv {
			if s, ok := v.(string); ok {
				conditionList[i] = s
			} else {
				conditionList[i] = fmt.Sprintf("%v", v)
			}
		}
	default:
		return false
	}

	// Check if attribute value matches any item in the list
	for _, item := range conditionList {
		if matchesValue(attributeValue, item) {
			return true
		}
	}
	return false
}

// matchesValue checks if an attribute value equals a string value with type coercion.
func matchesValue(attributeValue any, strValue string) bool {
	switch av := attributeValue.(type) {
	case string:
		return av == strValue
	case int, int8, int16, int32, int64:
		i64, err1 := internal.ToInt64(av)
		parsed, err2 := strconv.ParseInt(strValue, 10, 64)
		return err1 == nil && err2 == nil && i64 == parsed
	case uint, uint8, uint16, uint32, uint64:
		u64, err1 := internal.ToUint64(av)
		parsed, err2 := strconv.ParseUint(strValue, 10, 64)
		return err1 == nil && err2 == nil && u64 == parsed
	case float32, float64:
		f64, ok := internal.ToFloat64(av)
		parsed, err2 := strconv.ParseFloat(strValue, 64)
		return ok && err2 == nil && f64 == parsed
	case bool:
		parsed, err := strconv.ParseBool(strValue)
		return err == nil && av == parsed
	default:
		return fmt.Sprintf("%v", av) == strValue
	}
}

// evaluateNumericCondition evaluates numeric comparison operators.
func evaluateNumericCondition(attributeValue any, conditionValue any, operator conditionOperator) bool {
	attrNum, ok := internal.ToFloat64(attributeValue)
	if !ok {
		return false
	}

	condNum, ok := internal.ToFloat64(conditionValue)
	if !ok {
		return false
	}

	switch operator {
	case operatorGT:
		return attrNum > condNum
	case operatorGTE:
		return attrNum >= condNum
	case operatorLT:
		return attrNum < condNum
	case operatorLTE:
		return attrNum <= condNum
	default:
		return false
	}
}

// evaluateSplit determines if a split matches by evaluating all its shards.
func evaluateSplit(split *split, context map[string]any) bool {
	// All shards must match (AND logic)
	for _, shard := range split.Shards {
		if !evaluateShard(shard, context) {
			return false
		}
	}
	return true
}

// evaluateShard evaluates a shard using consistent hashing.
func evaluateShard(shard *shard, context map[string]any) bool {
	// Get targeting key from context
	targetingKey, ok := context[of.TargetingKey].(string)
	if !ok {
		return false
	}

	// Compute shard index using MD5 hash (matching Eppo's implementation)
	shardIndex := computeShardIndex(shard.Salt, targetingKey, shard.TotalShards)

	// Check if shard index falls within any of the ranges
	for _, shardRange := range shard.Ranges {
		if shardIndex >= shardRange.Start && shardIndex < shardRange.End {
			return true
		}
	}
	return false
}

// computeShardIndex computes the shard index using MD5 hash.
// This matches the Eppo SDK implementation for consistent behavior.
func computeShardIndex(salt, targetingKey string, totalShards int) int {
	input := salt + "-" + targetingKey
	hash := md5.Sum([]byte(input))
	// Use first 4 bytes of MD5 hash as uint32
	intVal := binary.BigEndian.Uint32(hash[:4])
	return int(int64(intVal) % int64(totalShards))
}

// validateVariantType checks if a variant value matches the expected flag type.
func validateVariantType(value any, expectedType valueType) error {
	switch expectedType {
	case valueTypeBoolean:
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("expected boolean, got %T", value)
		}
	case valueTypeString:
		if _, ok := value.(string); !ok {
			return fmt.Errorf("expected string, got %T", value)
		}
	case valueTypeInteger:
		// Accept int, int64, float64 (if whole number)
		switch v := value.(type) {
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
			return nil
		case float64:
			if v == float64(int64(v)) {
				return nil
			}
			return fmt.Errorf("expected integer, got float64 with decimal: %v", v)
		default:
			return fmt.Errorf("expected integer, got %T", value)
		}
	case valueTypeNumeric:
		// Accept any numeric type
		switch value.(type) {
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
			return nil
		default:
			return fmt.Errorf("expected numeric, got %T", value)
		}
	case valueTypeJSON:
		// JSON type accepts any value
		return nil
	default:
		return fmt.Errorf("unknown value type: %s", expectedType)
	}
	return nil
}
