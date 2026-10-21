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
	metadataHookFeaturePrefix        = "__dd_feature."
	metadataExposureFeaturePrefix    = "__dd_exposure_feature."
	metadataEvaluationFeaturePrefix  = "__dd_evaluation_feature."
	metadataHasEvaluationFeaturesKey = "__dd_has_evaluation_features"
)

// splitFeature is configuration metadata, not a targeting attribute.
type splitFeature struct {
	Key          string   `json:"key"`
	Value        any      `json:"value"`
	Destinations []string `json:"destinations"`
}

type splitFeatures []splitFeature

// UnmarshalJSON ignores malformed features without rejecting the flag.
func (f *splitFeatures) UnmarshalJSON(data []byte) error {
	*f = nil
	var entries []json.RawMessage
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil
	}
	for _, entry := range entries {
		var raw struct {
			Key          string `json:"key"`
			Value        any    `json:"value"`
			Destinations []any  `json:"destinations"`
		}
		if err := json.Unmarshal(entry, &raw); err != nil || raw.Key == "" {
			continue
		}
		switch raw.Value.(type) {
		case string, bool, float64:
		default:
			continue
		}
		var destinations []string
		for _, value := range raw.Destinations {
			if destination, ok := value.(string); ok && destination != "" {
				destinations = append(destinations, destination)
			}
		}
		if len(destinations) > 0 {
			*f = append(*f, splitFeature{raw.Key, raw.Value, destinations})
		}
	}
	return nil
}

func (f splitFeatures) addMetadata(metadata of.FlagMetadata) {
	if len(f) == 0 {
		return
	}
	seen := make(map[string]struct{}, len(f))
	for _, feature := range f {
		if _, duplicate := seen[feature.Key]; duplicate {
			continue
		}
		seen[feature.Key] = struct{}{}
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

var unknownFeatureDestinations = struct {
	sync.Mutex
	names map[string]struct{}
}{names: make(map[string]struct{})}

func warnUnknownFeatureDestination(destination string) {
	unknownFeatureDestinations.Lock()
	_, warned := unknownFeatureDestinations.names[destination]
	if warned || len(unknownFeatureDestinations.names) >= 64 {
		unknownFeatureDestinations.Unlock()
		return
	}
	unknownFeatureDestinations.names[destination] = struct{}{}
	unknownFeatureDestinations.Unlock()
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
