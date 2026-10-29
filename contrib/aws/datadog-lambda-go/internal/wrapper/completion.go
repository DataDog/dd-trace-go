// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package wrapper

import (
	"context"
	"fmt"
	"io"
	"sync"
)

type invocationCompletion struct {
	ctx       context.Context
	listeners []HandlerListener
	cleanup   func()
	once      sync.Once
}

func (c *invocationCompletion) finish(err error) {
	c.once.Do(func() {
		defer c.cleanup()
		for _, listener := range c.listeners {
			listener.HandlerFinished(c.ctx, err)
		}
	})
}

// recoverPanic must be deferred directly. AWS still receives the original panic
// value and remains responsible for reporting it and deciding whether to exit.
func (c *invocationCompletion) recoverPanic() {
	if value := recover(); value != nil {
		defer func() { panic(value) }()
		c.finish(fmt.Errorf("panic: %v", value))
	}
}

type invocationResponse struct {
	reader     io.Reader
	completion *invocationCompletion
	mu         sync.Mutex
	readErr    error
}

func (r *invocationResponse) Read(p []byte) (int, error) {
	defer r.completion.recoverPanic()
	n, err := r.reader.Read(p)
	if err != nil {
		r.mu.Lock()
		if r.readErr == nil {
			r.readErr = err
		}
		r.mu.Unlock()
	}
	return n, err
}

func (r *invocationResponse) Close() error {
	defer r.completion.recoverPanic()
	var err error
	if closer, ok := r.reader.(io.Closer); ok {
		err = closer.Close()
	}
	// AWS closes after its response POST finishes. Completing at EOF would
	// flush while that instrumented HTTP child span is still open, leaving
	// the invocation trace buffered until the next invocation.
	r.mu.Lock()
	finishErr := r.readErr
	r.mu.Unlock()
	if finishErr == io.EOF {
		finishErr = err
	} else if finishErr == nil {
		finishErr = err
		if finishErr == nil {
			finishErr = r.completion.ctx.Err()
		}
		if finishErr == nil {
			finishErr = io.ErrUnexpectedEOF
		}
	}
	r.completion.finish(finishErr)
	return err
}

type responseContentType interface {
	ContentType() string
}

type invocationResponseWithContentType struct {
	*invocationResponse
	contentType responseContentType
}

func (r *invocationResponseWithContentType) ContentType() string {
	defer r.completion.recoverPanic()
	return r.contentType.ContentType()
}

func (c *invocationCompletion) wrapResponse(reader io.Reader) io.Reader {
	// Always expose Close, including for readers without resources to release,
	// so AWS runs completion even if transport failure prevents reaching EOF.
	response := &invocationResponse{reader: reader, completion: c}
	if contentType, ok := reader.(responseContentType); ok {
		return &invocationResponseWithContentType{response, contentType}
	}
	return response
}
