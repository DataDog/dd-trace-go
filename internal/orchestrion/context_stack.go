// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package orchestrion

import "slices"

// contextStack is stored in the GLS slot of runtime.g inserted by orchestrion.
// It holds context values shared within a single goroutine.
// TODO: handle cross-goroutine context values
type contextStack map[any][]any

// getDDContextStack is a main way to access the GLS slot of runtime.g inserted by orchestrion. This function should not be
// called if the enabled variable is false.
func getDDContextStack() *contextStack {
	if gls := getDDGLS(); gls != nil {
		return gls.(*contextStack)
	}

	newStack := &contextStack{}
	setDDGLS(newStack)
	return newStack
}

// Peek returns the top context from the stack without removing it.
func (s *contextStack) Peek(key any) any {
	if s == nil || *s == nil {
		return nil
	}

	stack, ok := (*s)[key]
	if !ok || len(stack) == 0 {
		return nil
	}

	return (*s)[key][len(stack)-1]
}

// Push adds a context to the stack.
func (s *contextStack) Push(key, val any) {
	if s == nil || *s == nil {
		return
	}

	(*s)[key] = append((*s)[key], val)
}

// Pop removes the top context from the stack and returns it.
func (s *contextStack) Pop(key any) any {
	if s == nil || *s == nil {
		return nil
	}

	stack, ok := (*s)[key]
	if !ok || len(stack) == 0 {
		return nil
	}

	lastIdx := len(stack) - 1
	val := stack[lastIdx]
	// slices.Delete zeroes removed elements in the backing array,
	// allowing GC to collect popped values.
	stack = slices.Delete(stack, lastIdx, lastIdx+1)

	if len(stack) == 0 {
		delete(*s, key)
	} else {
		(*s)[key] = stack
	}

	return val
}
