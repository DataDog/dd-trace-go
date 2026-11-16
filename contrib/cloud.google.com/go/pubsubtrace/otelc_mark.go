// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package pubsubtrace

import (
	"context"
	"reflect"
	"sync"
)

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

// receiveHandlers holds the code pointers of the handlers the pubsub contrib's
// WrapReceiveHandler returns. Every handler it returns comes from one function
// literal, so they all share one code pointer.
var receiveHandlers sync.Map // uintptr -> struct{}

// MarkReceiveHandler records h as a handler that the pubsub contrib already
// traces.
//
// In an otelc build, the receive method of the Pub/Sub client also wraps its
// handler. The code otelc injects there leaves a marked handler as it is, so a
// message received through the contrib gets one span, not two.
func MarkReceiveHandler(h any) {
	if p, ok := funcPointer(h); ok {
		receiveHandlers.Store(p, struct{}{})
	}
}

// ReceiveHandlerTraced reports whether h was recorded by [MarkReceiveHandler].
func ReceiveHandlerTraced(h any) bool {
	p, ok := funcPointer(h)
	if !ok {
		return false
	}
	_, ok = receiveHandlers.Load(p)
	return ok
}

func funcPointer(h any) (uintptr, bool) {
	v := reflect.ValueOf(h)
	if v.Kind() != reflect.Func || v.IsNil() {
		return 0, false
	}
	return v.Pointer(), true
}
