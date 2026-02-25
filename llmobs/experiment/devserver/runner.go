// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package devserver

import (
	"maps"

	"github.com/DataDog/dd-trace-go/v2/llmobs/experiment"
)

// mergeConfig performs a shallow merge of overrides into defaults,
// returning a new map. Override values take precedence.
func mergeConfig(defaults, overrides map[string]any) map[string]any {
	if len(defaults) == 0 && len(overrides) == 0 {
		return nil
	}
	merged := make(map[string]any, len(defaults)+len(overrides))
	maps.Copy(merged, defaults)
	maps.Copy(merged, overrides)
	return merged
}

// filterEvaluators returns only the evaluators whose names appear in the given list.
// If names is empty, no evaluators are returned (opt-in behavior).
func filterEvaluators(all []experiment.Evaluator, names []string) []experiment.Evaluator {
	if len(names) == 0 {
		return nil
	}
	nameSet := make(map[string]struct{}, len(names))
	for _, n := range names {
		nameSet[n] = struct{}{}
	}
	filtered := make([]experiment.Evaluator, 0, len(names))
	for _, ev := range all {
		if _, ok := nameSet[ev.Name()]; ok {
			filtered = append(filtered, ev)
		}
	}
	return filtered
}
