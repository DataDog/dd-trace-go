// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"crypto/md5"
	"encoding/binary"
	"fmt"
	"maps"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	of "github.com/open-feature/go-sdk/openfeature"

	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
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
	// Metadata contains additional evaluation metadata for hooks
	Metadata map[string]any
}

const (
	defaultMaxDependencyDepth = 10
	maxMaxDependencyDepth     = 255

	metadataAllocationKey    = "dd.allocation.key"
	metadataDoLogKey         = "__dd_do_log"
	metadataSplitSerialIDKey = "__dd_split_serial_id"
	// metadataExposureAfterErrorKey carries an After failure to the Finally bridge.
	// TODO(open-feature/go-sdk#623): Remove when go.mod requires the fixed SDK release.
	metadataExposureAfterErrorKey = "__dd_exposure_after_error"
	// metadataExposureCacheHitKey carries the shared cache decision to customer hooks.
	metadataExposureCacheHitKey = "__dd_exposure_cache_hit"
	// metadataEvalTimeKey carries the evaluation timestamp (UnixMilli, int64). It is stamped in
	// DatadogProvider.evaluate at evaluation entry so EVP first/last bounds use eval-time. The
	// __dd_ prefix marks it as internal-only (never serialized to the wire); matches Java's
	// DDEvaluator.METADATA_EVAL_TIMESTAMP_MS.
	metadataEvalTimeKey = "__dd_eval_timestamp_ms"
	// metadataObserveFullEvaluationDataKey carries the environment's consent snapshot, stamped
	// in DatadogProvider.evaluate. Travels with the evaluation so a later Remote Config update
	// cannot retroactively change the policy at flush time. Unprefixed snake_case — this is the
	// cross-SDK contract key (confirmed in the PII RFC and the Java pilot); every SDK stamps and
	// reads consent under this exact key so the same identifier appears across SDK sources.
	metadataObserveFullEvaluationDataKey = "observe_full_evaluation_data"
	metadataDependencyExposuresKey       = "__dd_dependency_exposures"
)

type dependencyEvaluationCallback func(string, valueType, any, evaluationResult)

type dependencyExposureCandidate struct {
	FlagKey string
	Result  evaluationResult
}

type dependencyGraphError struct {
	flagKey string
	code    of.ErrorCode
	message string
	cause   error
}

func (e *dependencyGraphError) Error() string { return e.message }
func (e *dependencyGraphError) Unwrap() error { return e.cause }

// evaluateFlag evaluates a feature flag with the given context. The caller supplies the
// evaluation time (now) so a single timestamp is shared between the allocation time-window
// checks here and the EVP eval-time metadata stamped by DatadogProvider.evaluate — avoiding a
// second time.Now() on the evaluation path.
// It returns the variant value, reason, and any error that occurred.
func evaluateFlag(flag *flag, defaultValue any, context map[string]any, now time.Time, exclusion experimentationExclusion) evaluationResult {
	return evaluateFlagWithResolver(flag, defaultValue, context, now, exclusion, nil)
}

type dependencyResolver interface {
	Resolve(*flagEvaluation) (string, error)
	Checkpoint() dependencyCheckpoint
	Rollback(dependencyCheckpoint)
}

func evaluateFlagWithResolver(
	flag *flag,
	defaultValue any,
	context map[string]any,
	now time.Time,
	exclusion experimentationExclusion,
	resolver dependencyResolver,
) evaluationResult {
	if flag == nil {
		return evaluationResult{Value: defaultValue, Reason: of.DefaultReason}
	}
	// Check if flag is enabled
	if !flag.Enabled {
		return evaluationResult{
			Value:  defaultValue,
			Reason: of.DisabledReason,
		}
	}

	// Evaluate allocations in order - first match wins (using the caller-supplied eval time)
	for _, allocation := range flag.Allocations {
		// Exclude before targeting or sharding, without changing eligible subjects' buckets.
		if exclusion.active() && exclusion.excludes(allocation) {
			continue
		}
		checkpoint := checkpointDependency(resolver)
		split, matched, err := evaluateAllocationWithResolver(allocation, context, now, resolver)
		if err != nil {
			rollbackDependency(resolver, checkpoint)
			return evaluationResult{
				Value:  defaultValue,
				Reason: of.ErrorReason,
				Error:  err,
			}
		}
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
					Error:  fmt.Errorf("%w: variant type mismatch: %v", errParseError, err),
				}
			}

			// Reserve the split's HOOK keys and scalar event-feature snapshots, plus
			// allocation/serial/doLog, two provider stamps and the exposure decision.
			features := split.featureMetadata()
			metadata := make(map[string]any, 6+len(features))
			metadata[metadataAllocationKey] = allocation.Key

			// Get doLog value (defaults to true if not specified)
			doLog := true
			if allocation.DoLog != nil {
				doLog = *allocation.DoLog
			}
			metadata[metadataDoLogKey] = doLog

			if split.SerialID != nil {
				metadata[metadataSplitSerialIDKey] = *split.SerialID
			}
			maps.Copy(metadata, features)

			// Determine reason:
			//   rules matched                         → TARGETING_MATCH
			//   temporal allocation with one split   → DEFAULT
			//   no rules, shards used                 → SPLIT
			//   no rules, no shards                   → STATIC
			var reason of.Reason
			switch {
			case len(allocation.Rules) > 0:
				reason = of.TargetingMatchReason
			case (allocation.StartAt != nil || allocation.EndAt != nil) &&
				len(allocation.Splits) == 1 && len(split.Shards) == 0:
				reason = of.DefaultReason
			case len(split.Shards) > 0:
				reason = of.SplitReason
			default:
				reason = of.StaticReason
			}

			return evaluationResult{
				Value:      variant.Value,
				VariantKey: variant.Key,
				Reason:     reason,
				Metadata:   metadata,
			}
		}
		rollbackDependency(resolver, checkpoint)
	}

	// No allocations matched, return default
	return evaluationResult{
		Value:  defaultValue,
		Reason: of.DefaultReason,
	}
}

// evaluateConfiguredFlag evaluates a flag from a parsed configuration. Invalid
// flags return PARSE_ERROR. Missing flags return FLAG_NOT_FOUND.
func evaluateConfiguredFlag(
	config *universalFlagsConfiguration,
	flagKey string,
	defaultValue any,
	context map[string]any,
	now time.Time,
	exclusion experimentationExclusion,
) evaluationResult {
	return evaluateConfiguredFlagWithCallbacks(config, flagKey, defaultValue, context, now, exclusion, nil)
}

func evaluateConfiguredFlagWithCallbacks(
	config *universalFlagsConfiguration,
	flagKey string,
	defaultValue any,
	context map[string]any,
	now time.Time,
	exclusion experimentationExclusion,
	onDependencyEvaluation dependencyEvaluationCallback,
) evaluationResult {
	resolver := &lazyDependencyResolver{
		config:                 config,
		rootFlagKey:            flagKey,
		context:                context,
		now:                    now,
		exclusion:              exclusion,
		onDependencyEvaluation: onDependencyEvaluation,
	}
	result := evaluateConfiguredFlagOnce(config, flagKey, defaultValue, context, now, exclusion, resolver)
	if result.Error == nil && resolver.session != nil && len(resolver.session.exposures) > 0 {
		if result.Metadata == nil {
			result.Metadata = make(map[string]any, 1)
		}
		result.Metadata[metadataDependencyExposuresKey] = resolver.session.exposures
	}
	return result
}

func evaluateConfiguredFlagOnce(
	config *universalFlagsConfiguration,
	flagKey string,
	defaultValue any,
	context map[string]any,
	now time.Time,
	exclusion experimentationExclusion,
	resolver dependencyResolver,
) evaluationResult {
	flag, exists := config.Flags[flagKey]
	if exists {
		if exclusion.mode == ExperimentationOptOutFutureExperimentsOnly && flag.invalidActivation {
			return evaluationResult{
				Value:  defaultValue,
				Reason: of.ErrorReason,
				Error:  fmt.Errorf("%w: invalid randomizationActivatedAt for flag %q", errParseError, flagKey),
			}
		}
		return evaluateFlagWithResolver(flag, defaultValue, context, now, exclusion, resolver)
	}
	if configErr, invalid := config.invalidFlags[flagKey]; invalid {
		return evaluationResult{
			Value:  defaultValue,
			Reason: of.ErrorReason,
			Error:  fmt.Errorf("%w: invalid configuration for flag %q: %w", errParseError, flagKey, configErr),
		}
	}
	return evaluationResult{
		Value:  defaultValue,
		Reason: of.ErrorReason,
		Error:  fmt.Errorf("%w: %q", errFlagNotFound, flagKey),
	}
}

func configuredMaxDependencyDepth(config *universalFlagsConfiguration) (int, error) {
	if config.EvaluatorParams.maxDependencyDepthInvalid {
		return 0, fmt.Errorf("%w: maxDependencyDepth must be an integer from 0 through 255", errParseError)
	}
	if config.EvaluatorParams.MaxDependencyDepth == nil {
		return defaultMaxDependencyDepth, nil
	}
	depth := *config.EvaluatorParams.MaxDependencyDepth
	if depth < 0 || depth > maxMaxDependencyDepth {
		return 0, fmt.Errorf("%w: maxDependencyDepth must be an integer from 0 through 255", errParseError)
	}
	return depth, nil
}

// evaluateAllocation evaluates an allocation and returns the matching split if any.
func evaluateAllocation(allocation *allocation, context map[string]any, currentTime time.Time) (*split, bool, error) {
	return evaluateAllocationWithResolver(allocation, context, currentTime, nil)
}

func evaluateAllocationWithResolver(
	allocation *allocation,
	context map[string]any,
	currentTime time.Time,
	resolver dependencyResolver,
) (*split, bool, error) {
	// Check time window constraints
	if allocation.StartAt != nil && currentTime.Before(*allocation.StartAt) {
		return nil, false, nil
	}
	if allocation.EndAt != nil && currentTime.After(*allocation.EndAt) {
		return nil, false, nil
	}

	// Check if any rule matches (OR logic between rules)
	// If there are no rules, the allocation matches everyone
	ruleMatched := len(allocation.Rules) == 0
	for _, rule := range allocation.Rules {
		checkpoint := checkpointDependency(resolver)
		matched, err := evaluateRuleWithResolver(rule, context, resolver)
		if err != nil {
			rollbackDependency(resolver, checkpoint)
			return nil, false, err
		}
		if matched {
			ruleMatched = true
			break
		}
		rollbackDependency(resolver, checkpoint)
	}

	if !ruleMatched {
		return nil, false, nil
	}

	// Evaluate splits to determine which variant
	for _, split := range allocation.Splits {
		matched, err := evaluateSplit(split, context)
		if err != nil {
			return nil, false, err
		}
		if matched {
			return split, true, nil
		}
	}

	return nil, false, nil
}

// evaluateRule evaluates a rule by checking all conditions (AND logic).
func evaluateRule(rule *rule, context map[string]any) bool {
	matched, _ := evaluateRuleWithResolver(rule, context, nil)
	return matched
}

func evaluateRuleWithResolver(rule *rule, context map[string]any, resolver dependencyResolver) (bool, error) {
	for _, condition := range rule.Conditions {
		matched, err := evaluateConditionWithResolver(condition, context, resolver)
		if err != nil {
			return false, err
		}
		if !matched {
			return false, nil
		}
	}
	return true, nil
}

// evaluateCondition evaluates a single condition against the context.
func evaluateCondition(condition *condition, context map[string]any) bool {
	matched, _ := evaluateConditionWithResolver(condition, context, nil)
	return matched
}

func evaluateConditionWithResolver(
	condition *condition,
	context map[string]any,
	resolver dependencyResolver,
) (bool, error) {
	if condition.FlagEvaluation != nil {
		if resolver == nil {
			return false, nil
		}
		variantKey, err := resolver.Resolve(condition.FlagEvaluation)
		if err != nil {
			return false, err
		}
		switch condition.Operator {
		case operatorOneOf:
			return isOneOf(variantKey, condition.Value), nil
		case operatorNotOneOf:
			return !isOneOf(variantKey, condition.Value), nil
		default:
			return false, nil
		}
	}

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
			return false, nil
		}
		return isNull == expectedNull, nil
	}

	// For all other operators, attribute must exist
	if !exists || attributeValue == nil {
		return false, nil
	}

	switch condition.Operator {
	case operatorMatches:
		return matchesRegex(attributeValue, condition.Value), nil
	case operatorNotMatches:
		return !matchesRegex(attributeValue, condition.Value), nil
	case operatorOneOf:
		return isOneOf(attributeValue, condition.Value), nil
	case operatorNotOneOf:
		return !isOneOf(attributeValue, condition.Value), nil
	case operatorGT, operatorGTE, operatorLT, operatorLTE:
		return evaluateNumericCondition(attributeValue, condition.Value, condition.Operator), nil
	case operatorSemverEQ, operatorSemverNEQ, operatorSemverLT,
		operatorSemverLTE, operatorSemverGT, operatorSemverGTE:
		return evaluateSemverCondition(attributeValue, condition.semverComparand, condition.Operator), nil
	default:
		return false, nil
	}
}

type dependencyCheckpoint struct {
	exposureCount      int
	frameExposureCount int
	hasFrame           bool
}

type dependencyFrame struct {
	maxReachedDepth int
	exposureTrace   []dependencyExposureCandidate
}

type memoizedDependency struct {
	result          evaluationResult
	descendantDepth int
	exposureTrace   []dependencyExposureCandidate
}

type dependencyEvaluationSession struct {
	config                 *universalFlagsConfiguration
	context                map[string]any
	now                    time.Time
	exclusion              experimentationExclusion
	maxDepth               int
	active                 map[string]bool
	memo                   map[string]memoizedDependency
	frames                 []*dependencyFrame
	exposures              []dependencyExposureCandidate
	onDependencyEvaluation dependencyEvaluationCallback
}

// lazyDependencyResolver keeps the dependency session and all of its maps off
// the ordinary evaluation path until a flagEvaluation condition is reached.
type lazyDependencyResolver struct {
	config                 *universalFlagsConfiguration
	rootFlagKey            string
	context                map[string]any
	now                    time.Time
	exclusion              experimentationExclusion
	onDependencyEvaluation dependencyEvaluationCallback
	session                *dependencyEvaluationSession
}

func (r *lazyDependencyResolver) Resolve(ref *flagEvaluation) (string, error) {
	if r.session == nil {
		maxDepth, err := configuredMaxDependencyDepth(r.config)
		if err != nil {
			return "", &dependencyGraphError{
				flagKey: ref.Key,
				code:    of.ParseErrorCode,
				message: err.Error(),
				cause:   err,
			}
		}
		r.session = &dependencyEvaluationSession{
			config:                 r.config,
			context:                r.context,
			now:                    r.now,
			exclusion:              r.exclusion,
			maxDepth:               maxDepth,
			active:                 map[string]bool{r.rootFlagKey: true},
			memo:                   make(map[string]memoizedDependency),
			onDependencyEvaluation: r.onDependencyEvaluation,
		}
	}
	return r.session.Resolve(ref)
}

func (r *lazyDependencyResolver) Checkpoint() dependencyCheckpoint {
	if r.session == nil {
		return dependencyCheckpoint{}
	}
	return r.session.Checkpoint()
}

func (r *lazyDependencyResolver) Rollback(checkpoint dependencyCheckpoint) {
	if r.session != nil {
		r.session.Rollback(checkpoint)
	}
}

func checkpointDependency(resolver dependencyResolver) dependencyCheckpoint {
	if resolver == nil {
		return dependencyCheckpoint{}
	}
	return resolver.Checkpoint()
}

func rollbackDependency(resolver dependencyResolver, checkpoint dependencyCheckpoint) {
	if resolver != nil {
		resolver.Rollback(checkpoint)
	}
}

func (s *dependencyEvaluationSession) Checkpoint() dependencyCheckpoint {
	checkpoint := dependencyCheckpoint{exposureCount: len(s.exposures)}
	if len(s.frames) > 0 {
		checkpoint.hasFrame = true
		checkpoint.frameExposureCount = len(s.frames[len(s.frames)-1].exposureTrace)
	}
	return checkpoint
}

func (s *dependencyEvaluationSession) Rollback(checkpoint dependencyCheckpoint) {
	if checkpoint.exposureCount <= len(s.exposures) {
		s.exposures = s.exposures[:checkpoint.exposureCount]
	}
	if checkpoint.hasFrame && len(s.frames) > 0 {
		frame := s.frames[len(s.frames)-1]
		if checkpoint.frameExposureCount <= len(frame.exposureTrace) {
			frame.exposureTrace = frame.exposureTrace[:checkpoint.frameExposureCount]
		}
	}
}

func (s *dependencyEvaluationSession) Resolve(ref *flagEvaluation) (string, error) {
	return s.resolve(ref.Key, 1)
}

func (s *dependencyEvaluationSession) resolve(flagKey string, depth int) (string, error) {
	if depth > s.maxDepth {
		graphErr := dependencyDepthError(flagKey)
		_, memoized := s.memo[flagKey]
		if !memoized && !s.active[flagKey] {
			s.emitDepthError(flagKey, graphErr)
		}
		return "", graphErr
	}
	if s.active[flagKey] {
		return "", &dependencyGraphError{flagKey: flagKey, code: of.GeneralCode, message: "flag dependency cycle detected"}
	}
	if memoized, ok := s.memo[flagKey]; ok {
		if depth+memoized.descendantDepth > s.maxDepth {
			return "", dependencyDepthError(flagKey)
		}
		s.recordReachedDepth(depth + memoized.descendantDepth)
		s.recordExposureTrace(memoized.exposureTrace)
		return memoized.result.VariantKey, nil
	}

	flag, exists := s.config.Flags[flagKey]
	defaultValue := dependencyDefaultValue(flag)
	if !exists {
		if configErr, invalid := s.config.invalidFlags[flagKey]; invalid {
			result := evaluationResult{
				Value:  defaultValue,
				Reason: of.ErrorReason,
				Error:  fmt.Errorf("%w: invalid configuration for prerequisite flag %q: %w", errParseError, flagKey, configErr),
			}
			s.emit(flagKey, valueTypeBoolean, defaultValue, result)
			return "", &dependencyGraphError{flagKey: flagKey, code: of.ParseErrorCode, message: result.Error.Error(), cause: result.Error}
		}
		cause := fmt.Errorf("%w: prerequisite flag %q is missing", errFlagNotFound, flagKey)
		graphErr := &dependencyGraphError{
			flagKey: flagKey,
			code:    of.FlagNotFoundCode,
			message: "prerequisite flag is missing",
			cause:   cause,
		}
		result := evaluationResult{
			Value:  defaultValue,
			Reason: of.ErrorReason,
			Error:  graphErr,
		}
		s.emit(flagKey, valueTypeBoolean, defaultValue, result)
		return "", graphErr
	}

	s.active[flagKey] = true
	frame := &dependencyFrame{maxReachedDepth: depth}
	s.frames = append(s.frames, frame)
	frameActive := true
	defer func() {
		if frameActive {
			s.frames = s.frames[:len(s.frames)-1]
			delete(s.active, flagKey)
		}
	}()

	result := evaluateConfiguredFlagOnce(s.config, flagKey, defaultValue, s.context, s.now, s.exclusion,
		dependencyResolverAtDepth{session: s, depth: depth + 1})
	if result.Error != nil || result.Reason == of.ErrorReason {
		graphErr := dependencyErrorFromResult(flagKey, result)
		result.Value = defaultValue
		result.Reason = of.ErrorReason
		result.Error = graphErr
		s.emit(flagKey, flag.VariationType, defaultValue, result)
		return "", graphErr
	}
	if result.VariantKey == "" {
		graphErr := &dependencyGraphError{flagKey: flagKey, code: of.GeneralCode, message: "prerequisite flag evaluation returned no variant"}
		result.Value = defaultValue
		result.Reason = of.ErrorReason
		result.Error = graphErr
		s.emit(flagKey, flag.VariationType, defaultValue, result)
		return "", graphErr
	}

	candidate := dependencyExposureCandidate{FlagKey: flagKey, Result: withoutEvaluationValue(result)}
	appendExposureUnique(&frame.exposureTrace, candidate)
	memoized := memoizedDependency{
		result:          result,
		descendantDepth: frame.maxReachedDepth - depth,
		exposureTrace:   append([]dependencyExposureCandidate(nil), frame.exposureTrace...),
	}
	s.memo[flagKey] = memoized
	s.emit(flagKey, flag.VariationType, defaultValue, result)
	s.frames = s.frames[:len(s.frames)-1]
	delete(s.active, flagKey)
	frameActive = false
	s.recordReachedDepth(depth + memoized.descendantDepth)
	s.recordExposureTrace(memoized.exposureTrace)
	return result.VariantKey, nil
}

func dependencyDepthError(flagKey string) *dependencyGraphError {
	return &dependencyGraphError{
		flagKey: flagKey,
		code:    of.GeneralCode,
		message: "maximum flag dependency depth exceeded",
	}
}

func (s *dependencyEvaluationSession) emitDepthError(flagKey string, graphErr *dependencyGraphError) {
	flag := s.config.Flags[flagKey]
	flagType := valueTypeBoolean
	if flag != nil {
		flagType = flag.VariationType
	}
	defaultValue := dependencyDefaultValue(flag)
	s.emit(flagKey, flagType, defaultValue, evaluationResult{
		Value:  defaultValue,
		Reason: of.ErrorReason,
		Error:  graphErr,
	})
}

type dependencyResolverAtDepth struct {
	session *dependencyEvaluationSession
	depth   int
}

func (r dependencyResolverAtDepth) Resolve(ref *flagEvaluation) (string, error) {
	return r.session.resolve(ref.Key, r.depth)
}
func (r dependencyResolverAtDepth) Checkpoint() dependencyCheckpoint { return r.session.Checkpoint() }
func (r dependencyResolverAtDepth) Rollback(c dependencyCheckpoint)  { r.session.Rollback(c) }

func (s *dependencyEvaluationSession) recordReachedDepth(depth int) {
	if len(s.frames) > 0 && depth > s.frames[len(s.frames)-1].maxReachedDepth {
		s.frames[len(s.frames)-1].maxReachedDepth = depth
	}
}

func (s *dependencyEvaluationSession) recordExposureTrace(trace []dependencyExposureCandidate) {
	for _, candidate := range trace {
		appendExposureUnique(&s.exposures, candidate)
		if len(s.frames) > 0 {
			appendExposureUnique(&s.frames[len(s.frames)-1].exposureTrace, candidate)
		}
	}
}

func appendExposureUnique(target *[]dependencyExposureCandidate, candidate dependencyExposureCandidate) {
	for _, existing := range *target {
		if existing.FlagKey == candidate.FlagKey {
			return
		}
	}
	*target = append(*target, candidate)
}

func withoutEvaluationValue(result evaluationResult) evaluationResult {
	result.Value = nil
	return result
}

func (s *dependencyEvaluationSession) emit(flagKey string, flagType valueType, defaultValue any, result evaluationResult) {
	if result.Metadata == nil {
		result.Metadata = make(map[string]any, 2)
	}
	result.Metadata[metadataEvalTimeKey] = s.now.UnixMilli()
	result.Metadata[metadataObserveFullEvaluationDataKey] = s.config.ObserveFullEvaluationData
	if s.onDependencyEvaluation != nil {
		func() {
			defer func() { _ = recover() }()
			s.onDependencyEvaluation(flagKey, flagType, defaultValue, result)
		}()
	}
}

func dependencyErrorFromResult(flagKey string, result evaluationResult) *dependencyGraphError {
	code := errorCodeFor(result.Error)
	if code == "" {
		code = of.GeneralCode
	}
	message := "prerequisite flag evaluation failed"
	if result.Error != nil {
		message = result.Error.Error()
	}
	return &dependencyGraphError{flagKey: flagKey, code: code, message: message, cause: result.Error}
}

func dependencyDefaultValue(flag *flag) any {
	if flag == nil {
		return false
	}
	switch flag.VariationType {
	case valueTypeBoolean:
		return false
	case valueTypeInteger, valueTypeNumeric:
		return 0
	case valueTypeString:
		return ""
	case valueTypeJSON:
		return map[string]any{}
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
	// Go regular expressions are Unicode-aware by default and do not support
	// the explicit (?u) mode accepted by some other SDK runtimes.
	compiled, err := regexp.Compile(strings.TrimPrefix(pattern, "(?u)"))
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
			s, ok := v.(string)
			if !ok {
				return false
			}
			conditionList[i] = s
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

// evaluateSemverCondition evaluates semantic version comparison operators.
func evaluateSemverCondition(attributeValue any, comparand *parsedSemver, operator conditionOperator) bool {
	attribute, ok := attributeValue.(string)
	if !ok {
		return false
	}
	if comparand == nil {
		return false
	}

	parsedAttribute, ok := parseSemver(attribute)
	if !ok {
		return false
	}
	ordering := compareSemver(parsedAttribute, *comparand)

	switch operator {
	case operatorSemverEQ:
		return ordering == 0
	case operatorSemverNEQ:
		return ordering != 0
	case operatorSemverLT:
		return ordering < 0
	case operatorSemverLTE:
		return ordering <= 0
	case operatorSemverGT:
		return ordering > 0
	case operatorSemverGTE:
		return ordering >= 0
	default:
		return false
	}
}

// evaluateSplit determines if a split matches by evaluating all its shards.
func evaluateSplit(split *split, context map[string]any) (bool, error) {
	// All shards must match (AND logic)
	for _, shard := range split.Shards {
		matched, err := evaluateShard(shard, context)
		if err != nil {
			return false, err
		}
		if !matched {
			return false, nil
		}
	}
	return true, nil
}

// evaluateShard evaluates a shard using consistent hashing.
func evaluateShard(shard *shard, context map[string]any) (bool, error) {
	// Get targeting key from context
	targetingKey, ok := context[of.TargetingKey].(string)
	if !ok {
		return false, errTargetingKeyMissing
	}

	// Compute shard index using MD5 hash (matching Eppo's implementation)
	shardIndex := computeShardIndex(shard.Salt, targetingKey, shard.TotalShards)

	// Check if shard index falls within any of the ranges
	for _, shardRange := range shard.Ranges {
		if shardIndex >= shardRange.Start && shardIndex < shardRange.End {
			return true, nil
		}
	}
	return false, nil
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
