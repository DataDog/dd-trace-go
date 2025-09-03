/*
 * Unless explicitly stated otherwise all files in this repository are licensed
 * under the Apache License Version 2.0.
 *
 * This product includes software developed at Datadog (https://www.datadoghq.com/).
 * Copyright 2021 Datadog, Inc.
 */

package metrics

import "context"

type contextKeytype int

var metricsListenerKey = new(contextKeytype)

// GetListener retrieves the metrics listener from a context object.
func GetListener(ctx context.Context) *Listener {
	result := ctx.Value(metricsListenerKey)
	if result == nil {
		return nil
	}
	return result.(*Listener)
}

// AddListener adds a metrics listener to a context object
func AddListener(ctx context.Context, listener *Listener) context.Context {
	return context.WithValue(ctx, metricsListenerKey, listener)
}
