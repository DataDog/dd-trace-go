// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package ossec

import (
	"context"
	"os"
	"sync"

	"gopkg.in/DataDog/dd-trace-go.v1/appsec/events"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/ossec/types"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

var badInputContextOnce sync.Once

const readMask = os.O_RDONLY | os.O_RDWR

func ProtectOpen(ctx context.Context, path string, flags int) error {
	if flags&readMask == 0 { // We only care about read operations
		return nil
	}

	parent, _ := dyngo.FromContext(ctx)
	if parent == nil { // No parent operation => we can't monitor the request
		badInputContextOnce.Do(func() {
			log.Debug("appsec: file opening monitoring attempt ignored: could not find the handler " +
				"instrumentation metadata in the request context: the request handler is not being monitored by a " +
				"middleware function or the incoming request context has not be forwarded correctly to the `os.Open` call")
		})
		return nil
	}

	op := &types.OpenOperation{
		Operation: dyngo.NewOperation(parent),
	}

	var err *events.BlockingSecurityEvent
	dyngo.OnData(op, func(e *events.BlockingSecurityEvent) {
		err = e
	})

	dyngo.StartOperation(op, types.OpenOperationArgs{
		Path: path,
	})
	dyngo.FinishOperation(op, types.OpenOperationRes{})

	if err != nil {
		log.Debug("appsec: malicious local file inclusion attack blocked by the WAF on path: %s", path)
		return err
	}

	return nil
}
