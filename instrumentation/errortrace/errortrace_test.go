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

// Creates an additional level of callers around the error returned by createTestError
// Helps us test that TracerErrors contain all callers after an error is created.
func testErrorWrapper() *TracerError {
	return createTestError()
}

// Creates a new TracerError instance with default parameters (n = 32, skip = 0)
func createTestError() *TracerError {
	return New("Something wrong")
}

func TestWrap(t *testing.T) {
	t.Run("wrap nil", func(t *testing.T) {
		assert := assert.New(t)
		err := Wrap(nil, 0, 0)
		assert.Nil(err)
	})

	t.Run("wrap TracerError", func(t *testing.T) {
		assert := assert.New(t)
		err := createTestError()
		wrappedErr := Wrap(err, 0, 0)

		assert.NotNil(wrappedErr)
		assert.Equal(err, wrappedErr)
		assert.Equal(err.Error(), wrappedErr.Error())
		wrappedStack := wrappedErr.Format()
		originalStack := err.Format()
		assert.Equal(wrappedStack, originalStack)
	})

	t.Run("default", func(t *testing.T) {
		assert := assert.New(t)
		err := errors.New("msg")
		wrappedErr := Wrap(err, 0, 0)

		assert.NotNil(wrappedErr)
		assert.Equal("msg", wrappedErr.Error())
		assert.Equal(err, wrappedErr.Unwrap())
		stack := wrappedErr.Format()
		assert.NotNil(stack)
		assert.Greater(len(stack), 0)
	})

	t.Run("with Errorf", func(t *testing.T) {
		assert := assert.New(t)
		err := fmt.Errorf("val: %d", 1)
		wrappedErr := Wrap(err, 0, 0)

		assert.NotNil(wrappedErr)
		assert.Equal(err.Error(), wrappedErr.Error())
		stack := wrappedErr.Format()
		assert.NotNil(stack)
		assert.Greater(len(stack), 0)
	})
}

func TestErrorStack(t *testing.T) {
	t.Run("errortrace New", func(t *testing.T) {
		assert := assert.New(t)
		err := createTestError()
		stack := err.Format()
		assert.NotNil(stack)
		assert.Greater(len(stack), 0)
		assert.Contains(stack, "errortrace.createTestError")
		assert.Contains(stack, "errortrace.TestErrorStack")
		assert.Contains(stack, "testing.tRunner")
		assert.Contains(stack, "runtime.goexit")
	})

	t.Run("layered tracererror", func(t *testing.T) {
		assert := assert.New(t)
		err := testErrorWrapper()
		stack := err.Format()
		assert.NotNil(stack)
		assert.Greater(len(stack), 0)
		assert.Contains(stack, "errortrace.testErrorWrapper")
		assert.Contains(stack, "errortrace.createTestError")
		assert.Contains(stack, "errortrace.TestErrorStack")
		assert.Contains(stack, "testing.tRunner")
		assert.Contains(stack, "runtime.goexit")
	})

	t.Run("wrapped error", func(t *testing.T) {
		assert := assert.New(t)
		err := errors.New("msg")
		wrappedErr := Wrap(err, 0, 0)
		stack := wrappedErr.Format()
		assert.NotNil(stack)
		assert.Greater(len(stack), 0)
		assert.Contains(stack, "errortrace.TestErrorStack")
		assert.Contains(stack, "testing.tRunner")
		assert.Contains(stack, "runtime.goexit")
	})

	t.Run("skip 1", func(t *testing.T) {
		assert := assert.New(t)
		err := errors.New("msg")
		wrappedErr := Wrap(err, 0, 1)
		stack := wrappedErr.Format()
		assert.NotNil(stack)
		assert.Greater(len(stack), 0)
		assert.NotContains(stack, "errortrace.TestErrorStack")
		assert.Contains(stack, "testing.tRunner")
		assert.Contains(stack, "runtime.goexit")
	})

	t.Run("skip 2", func(t *testing.T) {
		assert := assert.New(t)
		err := errors.New("msg")
		wrappedErr := Wrap(err, 0, 2)
		stack := wrappedErr.Format()
		assert.NotNil(stack)
		assert.Greater(len(stack), 0)
		assert.NotContains(stack, "errortrace.TestErrorStack")
		assert.NotContains(stack, "testing.tRunner")
		assert.Contains(stack, "runtime.goexit")
	})

	t.Run("skip > num frames", func(t *testing.T) {
		assert := assert.New(t)
		err := errors.New("msg")
		wrappedErr := Wrap(err, 0, 3)
		stack := wrappedErr.Format()
		assert.Empty(stack)
	})

	t.Run("n = 1", func(t *testing.T) {
		assert := assert.New(t)
		err := errors.New("msg")
		wrappedErr := Wrap(err, 1, 0)
		stack := wrappedErr.Format()
		assert.NotNil(stack)
		assert.Greater(len(stack), 0)
		assert.Contains(stack, "errortrace.TestErrorStack")
		assert.NotContains(stack, "testing.tRunner")
		assert.NotContains(stack, "runtime.goexit")
	})

	t.Run("n = 2", func(t *testing.T) {
		assert := assert.New(t)
		err := errors.New("msg")
		wrappedErr := Wrap(err, 2, 0)
		stack := wrappedErr.Format()
		assert.NotNil(stack)
		assert.Greater(len(stack), 0)
		assert.Contains(stack, "errortrace.TestErrorStack")
		assert.Contains(stack, "testing.tRunner")
		assert.NotContains(stack, "runtime.goexit")
	})

	t.Run("skip == n", func(t *testing.T) {
		assert := assert.New(t)
		err := errors.New("msg")
		wrappedErr := Wrap(err, 1, 1)
		stack := wrappedErr.Format()
		assert.NotNil(stack)
		assert.Greater(len(stack), 0)
		assert.NotContains(stack, "errortrace.TestErrorStack")
		assert.Contains(stack, "testing.tRunner")
		assert.NotContains(stack, "runtime.goexit")
	})

	t.Run("invalid skip", func(t *testing.T) {
		assert := assert.New(t)
		err := errors.New("msg")
		wrappedErr := Wrap(err, 0, 100)
		stack := wrappedErr.Format()
		assert.Empty(stack)
	})
}

func TestUnwrap(t *testing.T) {
	t.Run("unwrap nil", func(t *testing.T) {
		assert := assert.New(t)
		err := Wrap(nil, 0, 0)
		unwrapped := err.Unwrap()
		assert.Nil(unwrapped)
	})

	t.Run("unwrap TracerError", func(t *testing.T) {
		assert := assert.New(t)
		err := errors.New("Something wrong")
		wrapped := Wrap(err, 0, 0)
		unwrapped := wrapped.Unwrap()
		assert.Equal(err, unwrapped)
	})
}

func TestErrorf(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		assert := assert.New(t)
		err := Errorf("")
		assert.NotNil(err)
		assert.Equal("", err.Error())
	})

	t.Run("single, non-error", func(t *testing.T) {
		assert := assert.New(t)
		err := Errorf("val: %d", 1)
		assert.NotNil(err)
		assert.Equal("val: 1", err.Error())
	})

	t.Run("%w, error", func(t *testing.T) {
		assert := assert.New(t)
		err := Errorf("val: %w", errors.New("Something wrong"))
		assert.NotNil(err)
		assert.Equal("val: Something wrong", err.Error())
	})

	t.Run("%w, TracerError", func(t *testing.T) {
		assert := assert.New(t)
		err := Errorf("val: %w", createTestError())
		assert.NotNil(err)
		assert.Equal("val: Something wrong", err.Error())
	})

	t.Run("multiple args, error", func(t *testing.T) {
		assert := assert.New(t)
		err := Errorf("val: %w, %w", errors.New("e1"), errors.New("e2"))
		assert.NotNil(err)
		assert.Equal("val: e1, e2", err.Error())
	})

	t.Run("multiple args, TracerError", func(t *testing.T) {
		assert := assert.New(t)
		err := Errorf("val: %w, %w", createTestError(), createTestError())
		assert.NotNil(err)
		assert.Equal("val: Something wrong, Something wrong", err.Error())
	})

	t.Run("multiple args, different types", func(t *testing.T) {
		assert := assert.New(t)
		err := Errorf("val: %w, %d, %w", errors.New("err"), 1, createTestError())
		assert.NotNil(err)
		assert.Equal("val: err, 1, Something wrong", err.Error())
	})
}
