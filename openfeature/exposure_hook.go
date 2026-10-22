// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"context"
	"maps"
	"sync"
	"sync/atomic"
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
	evaluationMetadata of.FlagMetadata // TODO(open-feature/go-sdk#623): Remove with the bridge.
	warnUnsupported    *sync.Once
}

// newExposureHook creates a new exposure tracking hook with the given writer
func newExposureHook(writer *exposureWriter) *exposureHook {
	return &exposureHook{
		writer:          writer,
		warnUnsupported: new(sync.Once),
	}
}

// TODO(open-feature/go-sdk#623): Remove the After/Error bridge and per-evaluation
// hook allocation when go.mod requires a release with corrected Finally details.
// The metadata map belongs to this evaluation, not to the exposure cache.
func (h *exposureHook) After(_ context.Context, ctx of.HookContext, details of.InterfaceEvaluationDetails, _ of.HookHints) error {
	if !h.supports(ctx) {
		return nil
	}
	h.evaluationMetadata = details.FlagMetadata
	return nil
}

func (h *exposureHook) Error(_ context.Context, ctx of.HookContext, err error, _ of.HookHints) {
	if !h.supports(ctx) {
		return
	}
	if h.evaluationMetadata != nil {
		h.evaluationMetadata[metadataExposureAfterErrorKey] = err.Error()
	}
}

// MultiProvider retains child hooks and completes them before outer After hooks.
// Guard every stage before accessing bridge state or consuming the cache.
// TODO: Enable composed providers once their hooks observe the final outcome.
func (h *exposureHook) supports(ctx of.HookContext) bool {
	if ctx.ProviderMetadata().Name == datadogProviderName {
		return true
	}
	h.warnUnsupported.Do(func() {
		log.Warn("openfeature: exposure delivery requires a directly registered Datadog provider; exposures are disabled for composed providers") //errtrack:ignore unsupported provider composition
	})
	return false
}

// TODO(open-feature/go-sdk#623): Remove this bridge when go.mod requires the fixed release.
func exposureFinalDetails(ctx of.HookContext, details of.InterfaceEvaluationDetails) of.InterfaceEvaluationDetails {
	if message, failed := details.FlagMetadata[metadataExposureAfterErrorKey].(string); failed && details.ErrorCode == "" {
		details.Value = ctx.DefaultValue()
		details.Reason = of.ErrorReason
		details.ErrorCode = of.GeneralCode
		details.ErrorMessage = message
	}
	return details
}

// Finally records only completed, successful evaluations. An application After
// hook can reject the provider result, so it must not consume the exposure cache.
// OpenFeature does not report After-hook panics as errors; see ExposureHook.
func (h *exposureHook) Finally(
	_ context.Context,
	hookContext of.HookContext,
	flagEvaluationDetails of.InterfaceEvaluationDetails,
	_ of.HookHints,
) {
	if !h.supports(hookContext) {
		return
	}
	// TODO(open-feature/go-sdk#623): Remove normalization after the dependency upgrade.
	flagEvaluationDetails = exposureFinalDetails(hookContext, flagEvaluationDetails)
	if flagEvaluationDetails.ErrorCode != "" || flagEvaluationDetails.Reason == of.ErrorReason {
		return
	}
	// The provider checks cancellation before resolving. Cancellation after a
	// successful resolution must not discard an exposure for the returned value.
	evaluation := ExposureEvaluation{Details: flagEvaluationDetails}
	if !evaluation.IsExposure() {
		log.Debug("openfeature: skipping ineligible exposure event for flag %q", hookContext.FlagKey())
		return
	}

	allocationKey, _ := evaluation.AllocationKey() // IsExposure established its presence.
	var serialID *uint32
	if id, ok := evaluation.SplitSerialID(); ok {
		serialID = new(id)
	}

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
		SerialID:    serialID,
		featuresKey: featureValuesKey(flagEvaluationDetails.FlagMetadata, metadataExposureFeaturesKey),
	}

	// Send to writer for buffering and eventual flushing
	// Decide once for all destinations. Customer hooks run after provider hooks
	// and read this stamp without consuming another cache entry.
	isNew, known := h.writer.appendWithDecision(event)
	if known {
		flagEvaluationDetails.FlagMetadata[metadataExposureCacheHitKey] = !isNew
	}

	log.Debug("openfeature: recorded exposure event for flag %q (allocation=%s, variant=%s, subject=%s)",
		hookContext.FlagKey(), allocationKey, flagEvaluationDetails.Variant, targetingKey)
}

// ExposureEvaluation describes a completed evaluation. Its cache decision
// is shared with Datadog's default exposure hook, even when Datadog logging is disabled.
// It does not confirm delivery to Datadog or to a customer destination.
type ExposureEvaluation struct {
	Context of.EvaluationContext
	Details of.InterfaceEvaluationDetails
}

// Features returns split metadata routed to customer hooks. The returned map is
// independent of the evaluation and other callbacks. Values are strings, booleans,
// or float64 numbers. An evaluation without hook features returns nil.
func (e ExposureEvaluation) Features() map[string]any {
	return splitFeatureValues(e.Details.FlagMetadata, metadataHookFeaturePrefix)
}

// AllocationKey returns the selected allocation key and whether it is present.
func (e ExposureEvaluation) AllocationKey() (string, bool) {
	key, ok := e.Details.FlagMetadata[metadataAllocationKey].(string)
	return key, ok
}

// SplitSerialID returns the selected split's serial ID and whether it is present.
// A present zero is distinct from an absent serial ID.
func (e ExposureEvaluation) SplitSerialID() (uint32, bool) {
	id, ok := e.Details.FlagMetadata[metadataSplitSerialIDKey].(uint32)
	return id, ok
}

// IsExposure reports whether the result selected an exposure-logging allocation.
func (e ExposureEvaluation) IsExposure() bool {
	_, hasAllocation := e.AllocationKey()
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
// non-exposures and cache hits. Register it with the OpenFeature API, a client, or
// an evaluation, retaining the provider's hooks. The callback runs synchronously: enqueue
// copied scalar fields for background delivery. It can run concurrently.
// Treat Details.Value as read-only. JSON maps and slices can share provider state;
// copy them before modification or background processing.
//
// Like the Datadog logger, this hook runs in the Finally stage. Evaluations with
// reported errors have no sendable exposure and do not consume the cache.
// OpenFeature v1.18.0 propagates application After-hook panics but still runs
// Finally without invoking Error hooks. Such evaluations can still consume the
// cache and send an exposure.
// A callback panic is recovered and does not change the flag result.
// Callbacks require a directly registered Datadog provider. They are disabled
// under MultiProvider, whose child hooks do not observe the final evaluation outcome.
type ExposureHook struct {
	of.UnimplementedHook
	callback        func(ExposureEvaluation)
	panicWarned     atomic.Bool
	warnUnsupported sync.Once
}

// NewExposureHook creates an exposure-aware customer hook. A nil callback is a no-op.
// Create each hook once and reuse it, including with of.WithHooks at invocation
// level. Warnings are deduplicated per hook; subsequent callback panics are logged
// at debug level. Independent callbacks each retain their own first warning.
func NewExposureHook(callback func(ExposureEvaluation)) *ExposureHook {
	return &ExposureHook{callback: callback}
}

// Finally invokes the callback with the shared exposure decision.
func (h *ExposureHook) Finally(_ context.Context, ctx of.HookContext, details of.InterfaceEvaluationDetails, _ of.HookHints) {
	if h.callback == nil {
		return
	}
	if ctx.ProviderMetadata().Name != datadogProviderName {
		h.warnUnsupported.Do(func() {
			log.Warn("openfeature: customer exposure callbacks require a directly registered Datadog provider; callbacks are disabled for composed providers") //errtrack:ignore unsupported provider composition
		})
		return
	}
	defer func() {
		if recover() != nil {
			if h.panicWarned.CompareAndSwap(false, true) {
				log.Warn("openfeature: customer exposure callback panicked for flag %q; later panics are logged at debug level", ctx.FlagKey()) //errtrack:ignore customer callback
			} else {
				log.Debug("openfeature: customer exposure callback panicked for flag %q", ctx.FlagKey())
			}
		}
	}()
	// TODO(open-feature/go-sdk#623): Remove normalization and marker cleanup when
	// go.mod requires the fixed release; retain the metadata copy for isolation.
	details = exposureFinalDetails(ctx, details)
	// Isolate metadata edits from the other callbacks and returned evaluation.
	details.FlagMetadata = maps.Clone(details.FlagMetadata)
	delete(details.FlagMetadata, metadataExposureAfterErrorKey)
	h.callback(ExposureEvaluation{Context: ctx.EvaluationContext(), Details: details})
}
