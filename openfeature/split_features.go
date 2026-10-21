// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package openfeature

import (
	"encoding/json"
	"strings"
	"sync"

	of "github.com/open-feature/go-sdk/openfeature"

	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

const (
	// These SDK metadata prefixes match Java's DDEvaluator. Event writers strip
	// the prefix; the original feature key appears in the event's features map.
	// metadataHookFeaturePrefix routes HOOK features to customer callbacks.
	metadataHookFeaturePrefix = "__dd_feature."
	// metadataExposureFeaturePrefix routes EXPOSURE features to Datadog exposures.
	metadataExposureFeaturePrefix = "__dd_exposure_feature."
	// metadataEvaluationFeaturePrefix routes EVALUATION features to evaluation events.
	metadataEvaluationFeaturePrefix = "__dd_evaluation_feature."
	// metadataHasEvaluationFeaturesKey avoids scanning metadata without EVALUATION features.
	metadataHasEvaluationFeaturesKey = "__dd_has_evaluation_features"
)

// splitFeature is configuration metadata, not a targeting attribute.
type splitFeature struct {
	Key          string   `json:"key"`
	Value        any      `json:"value"`
	Destinations []string `json:"destinations"`
}

// splitFeatures contains the first valid entry for each key after decoding.
type splitFeatures []splitFeature

// UnmarshalJSON ignores malformed features without rejecting the flag.
func (f *splitFeatures) UnmarshalJSON(data []byte) error {
	*f = nil
	var entries []json.RawMessage
	if err := json.Unmarshal(data, &entries); err != nil {
		log.Debug("openfeature: ignoring malformed split features: expected an array")
		return nil
	}
	seenKeys := make(map[string]struct{})
	for i, entry := range entries {
		// Bound metadata retained for evaluation and telemetry. Do not truncate keys
		// or values: truncation can merge distinct analysis dimensions.
		if len(*f) >= maxContextFields {
			log.Debug("openfeature: ignoring remaining split features: entry limit reached")
			break
		}
		var raw struct {
			Key          string `json:"key"`
			Value        any    `json:"value"`
			Destinations []any  `json:"destinations"`
		}
		if err := json.Unmarshal(entry, &raw); err != nil {
			log.Debug("openfeature: ignoring malformed split feature entry %d: invalid fields", i)
			continue
		}
		if raw.Key == "" || len(raw.Key) > maxFieldLength {
			log.Debug("openfeature: ignoring malformed split feature entry %d: invalid key length", i)
			continue
		}
		switch value := raw.Value.(type) {
		case string:
			if len(value) > maxFieldLength {
				log.Debug("openfeature: ignoring malformed split feature entry %d: string value too long", i)
				continue
			}
		case bool, float64:
		default:
			log.Debug("openfeature: ignoring malformed split feature entry %d: expected a scalar value", i)
			continue
		}
		var destinations []string
		for _, value := range raw.Destinations {
			if destination, ok := value.(string); ok && destination != "" {
				destinations = append(destinations, destination)
			}
		}
		if len(destinations) == 0 {
			log.Debug("openfeature: ignoring malformed split feature entry %d: no destinations", i)
			continue
		}
		if _, duplicate := seenKeys[raw.Key]; duplicate {
			continue
		}
		seenKeys[raw.Key] = struct{}{}
		*f = append(*f, splitFeature{raw.Key, raw.Value, destinations})
	}
	return nil
}

func (f splitFeatures) addMetadata(metadata of.FlagMetadata) {
	if len(f) == 0 {
		return
	}
	for _, feature := range f {
		for _, destination := range feature.Destinations {
			var prefix string
			switch destination {
			case "HOOK":
				prefix = metadataHookFeaturePrefix
			case "EXPOSURE":
				prefix = metadataExposureFeaturePrefix
			case "EVALUATION":
				prefix = metadataEvaluationFeaturePrefix
				metadata[metadataHasEvaluationFeaturesKey] = true
			default:
				warnUnknownFeatureDestination(destination)
				continue
			}
			metadata[prefix+feature.Key] = feature.Value
		}
	}
}

type warnedFeatureDestinations struct {
	mu    sync.Mutex
	names map[string]struct{} // +checklocks:mu
}

var unknownFeatureDestinations = warnedFeatureDestinations{names: make(map[string]struct{})}

func warnUnknownFeatureDestination(destination string) {
	unknownFeatureDestinations.mu.Lock()
	_, warned := unknownFeatureDestinations.names[destination]
	if warned || len(unknownFeatureDestinations.names) >= 64 {
		unknownFeatureDestinations.mu.Unlock()
		return
	}
	unknownFeatureDestinations.names[destination] = struct{}{}
	unknownFeatureDestinations.mu.Unlock()
	log.Warn("openfeature: ignoring unknown feature destination %q", destination) //errtrack:ignore configuration compatibility
}

func evaluationFeatureValues(metadata of.FlagMetadata) map[string]any {
	if present, _ := metadata[metadataHasEvaluationFeaturesKey].(bool); !present {
		return nil
	}
	return splitFeatureValues(metadata, metadataEvaluationFeaturePrefix)
}

// splitFeatureValues returns an independent map containing only scalar values.
func splitFeatureValues(metadata of.FlagMetadata, prefix string) map[string]any {
	var values map[string]any
	for key, value := range metadata {
		if !strings.HasPrefix(key, prefix) || len(key) == len(prefix) {
			continue
		}
		switch value.(type) {
		case string, bool, float64:
			if values == nil {
				values = make(map[string]any)
			}
			values[strings.TrimPrefix(key, prefix)] = value
		}
	}
	return values
}
