// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package openfeature

import "context"

// Evaluator evaluates an object value with targeting context.
type Evaluator func(context.Context, string, string, map[string]any) (any, error)

// NewEvaluator is registered by the Datadog OpenFeature package when imported.
var NewEvaluator func(domain string) (Evaluator, error)
