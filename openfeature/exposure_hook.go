// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"context"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/log"
	of "github.com/open-feature/go-sdk/openfeature"
)

const (
	// Metadata keys for exposure tracking
	metadataAllocationKey = "dd.allocation.key"
	metadataDoLog         = "dd.doLog"
)

// exposureHook implements the OpenFeature Hook interface to track feature flag exposures.
// It captures evaluation details and sends them to the exposure writer for reporting
// to Datadog's event platform intake.
type exposureHook struct {
	of.UnimplementedHook
	writer *exposureWriter
}

// newExposureHook creates a new exposure tracking hook with the given writer
func newExposureHook(writer *exposureWriter) *exposureHook {
	return &exposureHook{
		writer: writer,
	}
}

// After is called after a successful flag evaluation.
// It extracts the necessary information from the evaluation details and sends
// an exposure event to the writer if doLog is true.
func (h *exposureHook) After(
	ctx context.Context,
	hookContext of.HookContext,
	flagEvaluationDetails of.InterfaceEvaluationDetails,
	hookHints of.HookHints,
) error {
	// Check if we should log this exposure
	if !h.shouldLog(flagEvaluationDetails.FlagMetadata) {
		log.Debug("openfeature: skipping exposure event (doLog=false) for flag %q", hookContext.FlagKey())
		return nil
	}

	// Get allocation key from metadata
	allocationKey, ok := h.getAllocationKey(flagEvaluationDetails.FlagMetadata)
	if !ok {
		log.Debug("openfeature: skipping exposure event (no allocation key) for flag %q", hookContext.FlagKey())
		return nil
	}

	// Get targeting key (subject ID) from evaluation context
	evalContext := hookContext.EvaluationContext()
	targetingKey := evalContext.TargetingKey()
	if targetingKey == "" {
		log.Debug("openfeature: skipping exposure event (no targeting key) for flag %q", hookContext.FlagKey())
		return nil
	}

	// Build flat context from evaluation context
	flatContext := make(map[string]any)
	flatContext[of.TargetingKey] = targetingKey
	for k, v := range evalContext.Attributes() {
		flatContext[k] = v
	}

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
			Type:       "",                // Type is optional
			Attributes: subjectAttrs,      // Flattened, primitive-only attributes
		},
	}

	// Send to writer for buffering and eventual flushing
	h.writer.append(event)

	log.Debug("openfeature: recorded exposure event for flag %q (allocation=%s, variant=%s, subject=%s)",
		hookContext.FlagKey(), allocationKey, flagEvaluationDetails.Variant, targetingKey)

	return nil
}

// shouldLog checks if the exposure should be logged based on the doLog metadata flag
func (h *exposureHook) shouldLog(metadata of.FlagMetadata) bool {
	if metadata == nil {
		// Default to true if no metadata present
		return true
	}

	doLog, ok := metadata[metadataDoLog]
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
