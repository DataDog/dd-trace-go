// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package errortrace

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWrap(t *testing.T) {
	t.Run("wrap nil", func(t *testing.T) {
		err := Wrap(nil, 0, 0)
		assert.Nil(t, err)
	})

	t.Run("wrap TracerError", func(t *testing.T) {
		err := Wrap(errors.New("inner"), 0, 0)
		wrappedErr := Wrap(err, 0, 0)

		assert.NotNil(t, wrappedErr)
		assert.Equal(t, err, wrappedErr)
		assert.Equal(t, err.Error(), wrappedErr.Error())
		wrappedStack := wrappedErr.Stack()
		originalStack := err.Stack()
		assert.Equal(t, wrappedStack.String(), originalStack.String())
	})

	t.Run("default", func(t *testing.T) {
		err := errors.New("msg")
		wrappedErr := Wrap(err, 0, 0)

		assert.NotNil(t, wrappedErr)
		assert.Equal(t, "msg", wrappedErr.Error())
		assert.Equal(t, err, wrappedErr.Unwrap())
		stack := wrappedErr.Stack()
		assert.NotNil(t, stack)
		assert.Greater(t, stack.Len(), 0)
	})

	t.Run("with Errorf", func(t *testing.T) {
		err := fmt.Errorf("val: %d", 1)
		wrappedErr := Wrap(err, 0, 0)

		assert.NotNil(t, wrappedErr)
		assert.Equal(t, err.Error(), wrappedErr.Error())
		stack := wrappedErr.Stack()
		assert.NotNil(t, stack)
		assert.Greater(t, stack.Len(), 0)
	})
}

func TestErrorStack(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		err := errors.New("msg")
		wrappedErr := Wrap(err, 0, 0)
		stack := wrappedErr.Stack()
		assert.NotNil(t, stack)
		assert.Greater(t, stack.Len(), 0)
		assert.Contains(t, stack.String(), "errortrace.TestErrorStack")
		assert.Contains(t, stack.String(), "testing.tRunner")
		assert.Contains(t, stack.String(), "runtime.goexit")
	})

	t.Run("skip 1", func(t *testing.T) {
		err := errors.New("msg")
		wrappedErr := Wrap(err, 0, 1)
		stack := wrappedErr.Stack()
		assert.NotNil(t, stack)
		assert.Greater(t, stack.Len(), 0)
		assert.NotContains(t, stack.String(), "errortrace.TestErrorStack")
		assert.Contains(t, stack.String(), "testing.tRunner")
		assert.Contains(t, stack.String(), "runtime.goexit")
	})

	t.Run("skip 2", func(t *testing.T) {
		err := errors.New("msg")
		wrappedErr := Wrap(err, 0, 2)
		stack := wrappedErr.Stack()
		assert.NotNil(t, stack)
		assert.Greater(t, stack.Len(), 0)
		assert.NotContains(t, stack.String(), "errortrace.TestErrorStack")
		assert.NotContains(t, stack.String(), "testing.tRunner")
		assert.Contains(t, stack.String(), "runtime.goexit")
	})

	t.Run("skip 3", func(t *testing.T) {
		err := errors.New("msg")
		wrappedErr := Wrap(err, 0, 3)
		stack := wrappedErr.Stack()
		assert.NotNil(t, stack)
	})

	t.Run("n = 1", func(t *testing.T) {
		err := errors.New("msg")
		wrappedErr := Wrap(err, 1, 0)
		stack := wrappedErr.Stack()
		assert.NotNil(t, stack)
		assert.Greater(t, stack.Len(), 0)
		assert.Contains(t, stack.String(), "errortrace.TestErrorStack")
		assert.NotContains(t, stack.String(), "testing.tRunner")
		assert.NotContains(t, stack.String(), "runtime.goexit")
	})

	t.Run("n = 2", func(t *testing.T) {
		err := errors.New("msg")
		wrappedErr := Wrap(err, 1, 0)
		stack := wrappedErr.Stack()
		assert.NotNil(t, stack)
		assert.Greater(t, stack.Len(), 0)
		assert.Contains(t, stack.String(), "errortrace.TestErrorStack")
		assert.Contains(t, stack.String(), "testing.tRunner")
		assert.NotContains(t, stack.String(), "runtime.goexit")
	})

	t.Run("skip == n", func(t *testing.T) {
		err := errors.New("msg")
		wrappedErr := Wrap(err, 1, 1)
		stack := wrappedErr.Stack()
		assert.NotNil(t, stack)
		assert.Greater(t, stack.Len(), 0)
		assert.NotContains(t, stack.String(), "errortrace.TestErrorStack")
		assert.Contains(t, stack.String(), "testing.tRunner")
		assert.NotContains(t, stack.String(), "runtime.goexit")
	})

	t.Run("skip > n", func(t *testing.T) {
		err := errors.New("msg")
		wrappedErr := Wrap(err, 0, 1)
		stack := wrappedErr.Stack()
		assert.Nil(t, stack)
	})

	t.Run("invalid skip", func(t *testing.T) {
		err := errors.New("msg")
		wrappedErr := Wrap(err, 0, 100)
		stack := wrappedErr.Stack()
		assert.Nil(t, stack)
	})
}
