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
	// metadataHookFeaturePrefix matches Java's DDEvaluator. Features strips it.
	metadataHookFeaturePrefix = "__dd_feature."
	// Event features cross the OpenFeature boundary as canonical JSON strings.
	// FlagMetadata permits scalars only, so it cannot hold private maps or pointers.
	metadataExposureFeaturesKey   = "__dd_exposure_features"
	metadataEvaluationFeaturesKey = "__dd_evaluation_features"
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

// compiledSplitFeatures owns immutable, preboxed metadata. Configuration splits
// are immutable after publication; concurrent first uses may compile equal copies.
type compiledSplitFeatures struct {
	metadata of.FlagMetadata
}

func (s *split) featureMetadata() of.FlagMetadata {
	if len(s.Features) == 0 {
		return nil
	}
	c := s.compiledFeatures.Load()
	if c == nil {
		c = s.Features.compile()
		if !s.compiledFeatures.CompareAndSwap(nil, c) {
			c = s.compiledFeatures.Load()
		}
	}
	return c.metadata
}

func (f splitFeatures) compile() *compiledSplitFeatures {
	c := &compiledSplitFeatures{metadata: make(of.FlagMetadata)}
	exposure := make(map[string]any)
	evaluation := make(map[string]any)
	for _, feature := range f {
		for _, destination := range feature.Destinations {
			switch destination {
			case "HOOK":
				c.metadata[metadataHookFeaturePrefix+feature.Key] = feature.Value
			case "EXPOSURE":
				exposure[feature.Key] = feature.Value
			case "EVALUATION":
				evaluation[feature.Key] = feature.Value
			default:
				warnUnknownFeatureDestination(destination)
			}
		}
	}
	if key := encodeFeatureValues(exposure); key != "" {
		c.metadata[metadataExposureFeaturesKey] = key
	}
	if key := encodeFeatureValues(evaluation); key != "" {
		c.metadata[metadataEvaluationFeaturesKey] = key
	}
	return c
}

// JSON sorts map keys and preserves the scalar types accepted by the parser.
// The complete encoding is both the snapshot and its collision-free aggregation
// key. Queued evaluations never have to look up a later configuration snapshot.
func encodeFeatureValues(values map[string]any) string {
	if len(values) == 0 {
		return ""
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func featureValuesKey(metadata of.FlagMetadata, key string) string {
	encoded, _ := metadata[key].(string)
	return encoded
}

// featureValuesCache shares read-only maps across events in one flush window.
// Writers call values only when admitting a new event/bucket, under their lock,
// and discard the cache on flush. Its size is bounded by the retained events.
type featureValuesCache map[string]map[string]any

func (c *featureValuesCache) values(encoded string) map[string]any {
	if encoded == "" {
		return nil
	}
	if values, ok := (*c)[encoded]; ok {
		return values
	}
	var values map[string]any
	if err := json.Unmarshal([]byte(encoded), &values); err != nil {
		return nil
	}
	// Application hooks can edit metadata. Keep event features scalar even then.
	for key, value := range values {
		switch value.(type) {
		case string, bool, float64:
		default:
			delete(values, key)
		}
	}
	if *c == nil {
		*c = make(featureValuesCache)
	}
	(*c)[encoded] = values
	return values
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
