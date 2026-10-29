// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package pubsubtrace

import "context"

type publishTracedKey struct{}

// MarkPublishTraced tags ctx as a publish that the pubsub contrib already
// traces.
//
// In an otelc build, the publish method of the Pub/Sub client also starts a
// span, and the contrib's Publish calls that method. The code otelc injects
// there skips a marked context, so a message published through the contrib
// gets one span, not two.
func MarkPublishTraced(ctx context.Context) context.Context {
	return context.WithValue(ctx, publishTracedKey{}, struct{}{})
}

// PublishTraced reports whether ctx was tagged by [MarkPublishTraced].
func PublishTraced(ctx context.Context) bool {
	return ctx != nil && ctx.Value(publishTracedKey{}) != nil
}
