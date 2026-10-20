// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"context"
	"maps"
	"sync"
	"time"

	of "github.com/open-feature/go-sdk/openfeature"

	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

// exposureHook implements the OpenFeature Hook interface to track feature flag exposures.
// It captures evaluation details and sends them to the exposure writer for reporting
// to Datadog's event platform intake.
type exposureHook struct {
	of.UnimplementedHook
	writer             *exposureWriter
	evaluationMetadata of.FlagMetadata
}

// newExposureHook creates a new exposure tracking hook with the given writer
func newExposureHook(writer *exposureWriter) *exposureHook {
	return &exposureHook{
		writer: writer,
	}
}

// TODO(open-feature/go-sdk#623): Remove the After/Error bridge and per-evaluation
// hook allocation after upgrading to a release with corrected Finally details.
// The metadata map belongs to this evaluation, not to the exposure cache.
func (h *exposureHook) After(_ context.Context, _ of.HookContext, details of.InterfaceEvaluationDetails, _ of.HookHints) error {
	h.evaluationMetadata = details.FlagMetadata
	return nil
}

func (h *exposureHook) Error(context.Context, of.HookContext, error, of.HookHints) {
	if h.evaluationMetadata != nil {
		h.evaluationMetadata[metadataExposureAfterErrorKey] = true
	}
}

const metadataExposureAfterErrorKey = "__dd_exposure_after_error"

func exposureFinalDetails(ctx of.HookContext, details of.InterfaceEvaluationDetails) of.InterfaceEvaluationDetails {
	if failed, _ := details.FlagMetadata[metadataExposureAfterErrorKey].(bool); failed && details.ErrorCode == "" {
		details.Value = ctx.DefaultValue()
		details.Reason = of.ErrorReason
		details.ErrorCode = of.GeneralCode
		details.ErrorMessage = "after hook failed"
	}
	return details
}

// Finally records only completed, successful evaluations. An application After
// hook can reject the provider result, so it must not consume the exposure cache.
func (h *exposureHook) Finally(
	ctx context.Context,
	hookContext of.HookContext,
	flagEvaluationDetails of.InterfaceEvaluationDetails,
	_ of.HookHints,
) {
	flagEvaluationDetails = exposureFinalDetails(hookContext, flagEvaluationDetails)
	if flagEvaluationDetails.ErrorCode != "" || flagEvaluationDetails.Reason == of.ErrorReason {
		return
	}
	// Reporting must not change the evaluation result, even after cancellation.
	select {
	case <-ctx.Done():
		return
	default:
	}

	// Check if we should log this exposure
	if !h.shouldLog(flagEvaluationDetails.FlagMetadata) {
		log.Debug("openfeature: skipping exposure event (doLog=false) for flag %q", hookContext.FlagKey())
		return
	}

	// Get allocation key from metadata
	allocationKey, ok := h.getAllocationKey(flagEvaluationDetails.FlagMetadata)
	if !ok {
		log.Debug("openfeature: skipping exposure event (no allocation key) for flag %q", hookContext.FlagKey())
		return
	}

	serialID := h.getSerialID(flagEvaluationDetails.FlagMetadata)

	// Get targeting key (subject ID) from evaluation context
	evalContext := hookContext.EvaluationContext()
	targetingKey := evalContext.TargetingKey()

	// Build flat context from evaluation context
	flatContext := make(map[string]any)
	flatContext[of.TargetingKey] = targetingKey
	maps.Copy(flatContext, evalContext.Attributes())

	// Flatten attributes for exposure event
	flattenedAttrs := flattenContext(flatContext)

	// Extract only primitive attributes for the subject
	subjectAttrs := extractPrimitiveAttributes(flattenedAttrs)

	// Create exposure event
	event := exposureEvent{
		Timestamp: time.Now().UnixMilli(),
		Allocation: exposureAllocation{
			Key: allocationKey,
		},
		Flag: exposureFlag{
			Key: hookContext.FlagKey(),
		},
		Variant: exposureVariant{
			Key: flagEvaluationDetails.Variant,
		},
		Subject: exposureSubject{
			ID:         targetingKey,
			Type:       "",           // Type is optional
			Attributes: subjectAttrs, // Flattened, primitive-only attributes
		},
		SerialID: serialID,
	}

	// Send to writer for buffering and eventual flushing
	// Decide once for all destinations. Customer hooks run after provider hooks
	// and read this stamp without consuming another cache entry.
	isNew := h.writer.append(event)
	if flagEvaluationDetails.FlagMetadata != nil {
		flagEvaluationDetails.FlagMetadata[metadataExposureCacheHitKey] = !isNew
	}

	log.Debug("openfeature: recorded exposure event for flag %q (allocation=%s, variant=%s, subject=%s)",
		hookContext.FlagKey(), allocationKey, flagEvaluationDetails.Variant, targetingKey)
}

const metadataExposureCacheHitKey = "__dd_exposure_cache_hit"

// ExposureEvaluation describes a completed evaluation. Its cache decision
// is shared with Datadog's default exposure hook, even when Datadog logging is disabled.
// It does not confirm delivery to Datadog or to a customer destination.
type ExposureEvaluation struct {
	Context of.EvaluationContext
	Details of.InterfaceEvaluationDetails
}

// IsExposure reports whether the result selected an exposure-logging allocation.
func (e ExposureEvaluation) IsExposure() bool {
	_, hasAllocation := e.Details.FlagMetadata[metadataAllocationKey].(string)
	doLog, _ := e.Details.FlagMetadata[metadataDoLogKey].(bool)
	return e.Details.ErrorCode == "" && e.Details.Reason != of.ErrorReason &&
		e.Details.Variant != "" && hasAllocation && doLog
}

// IsCacheHit reports whether this exposure repeats the cached allocation, variant
// and optional split serial ID for this flag and subject.
func (e ExposureEvaluation) IsCacheHit() bool {
	hit, _ := e.Details.FlagMetadata[metadataExposureCacheHitKey].(bool)
	return e.IsExposure() && hit
}

// ShouldSend reports an eligible cache miss. False also means the cache decision
// is unavailable, for example when Datadog's provider hooks did not run.
func (e ExposureEvaluation) ShouldSend() bool {
	hit, known := e.Details.FlagMetadata[metadataExposureCacheHitKey].(bool)
	return e.IsExposure() && known && !hit
}

// ExposureHook calls customer code after completed evaluations, including errors,
// non-exposures and cache hits. Register it with an OpenFeature client or evaluation,
// not by replacing the provider's hooks. The callback runs synchronously: enqueue
// copied scalar fields for background delivery. It can run concurrently.
//
// Like the Datadog logger, this hook runs in the Finally stage. Failed evaluations
// never have a sendable exposure and do not consume the deduplication cache.
// A callback panic is recovered and does not change the flag result.
type ExposureHook struct {
	of.UnimplementedHook
	callback func(ExposureEvaluation)
	warnOnce sync.Once
}

// NewExposureHook creates an exposure-aware customer hook. A nil callback is a no-op.
func NewExposureHook(callback func(ExposureEvaluation)) *ExposureHook {
	return &ExposureHook{callback: callback}
}

// Finally invokes the callback with the shared exposure decision.
func (h *ExposureHook) Finally(_ context.Context, ctx of.HookContext, details of.InterfaceEvaluationDetails, _ of.HookHints) {
	if h.callback == nil {
		return
	}
	defer func() {
		if recover() != nil {
			h.warnOnce.Do(func() {
				log.Warn("openfeature: customer exposure callback panicked; later failures are suppressed") //errtrack:ignore customer callback
			})
		}
	}()
	details = exposureFinalDetails(ctx, details)
	// Isolate metadata edits from the other callbacks and returned evaluation.
	details.FlagMetadata = maps.Clone(details.FlagMetadata)
	delete(details.FlagMetadata, metadataExposureAfterErrorKey)
	h.callback(ExposureEvaluation{Context: ctx.EvaluationContext(), Details: details})
}

// shouldLog checks if the exposure should be logged based on the doLog metadata flag
func (h *exposureHook) shouldLog(metadata of.FlagMetadata) bool {
	if metadata == nil {
		// Default to true if no metadata present
		return true
	}

	doLog, ok := metadata[metadataDoLogKey]
	if !ok {
		// Default to true if doLog not specified
		return true
	}

	// Check if it's a boolean
	doLogBool, ok := doLog.(bool)
	if !ok {
		log.Debug("openfeature: doLog metadata is not a boolean, defaulting to true")
		return true
	}

	return doLogBool
}

// getSerialID extracts the split serial ID from flag metadata
func (h *exposureHook) getSerialID(metadata of.FlagMetadata) *uint32 {
	if metadata == nil {
		return nil
	}

	raw, ok := metadata[metadataSplitSerialIDKey]
	if !ok {
		return nil
	}

	serialID, ok := raw.(uint32)
	if !ok {
		log.Debug("openfeature: serial id metadata is not a uint32")
		return nil
	}

	return &serialID
}

// getAllocationKey extracts the allocation key from flag metadata
func (h *exposureHook) getAllocationKey(metadata of.FlagMetadata) (string, bool) {
	if metadata == nil {
		return "", false
	}

	allocationKey, ok := metadata[metadataAllocationKey]
	if !ok {
		return "", false
	}

	// Check if it's a string
	allocationKeyStr, ok := allocationKey.(string)
	if !ok {
		log.Debug("openfeature: allocation key metadata is not a string")
		return "", false
	}

	return allocationKeyStr, true
}
