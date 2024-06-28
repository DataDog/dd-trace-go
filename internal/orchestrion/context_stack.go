// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package orchestrion

func getDDContextStack() *contextStack {
	if gls := getDDGLS(); gls != nil {
		return gls.(*contextStack)
	}

	newStack := new(contextStack)
	setDDGLS(newStack)
	return newStack
}

type contextStack map[any][]any

// Peek returns the top context from the stack without removing it.
func (s *contextStack) Peek(key any) any {
	if len(*s) == 0 {
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
	(*s)[key] = append((*s)[key], val)
}

// Pop removes the top context from the stack and returns it.
func (s *contextStack) Pop(key any) any {
	if len(*s) == 0 {
		return nil
	}

	stack, ok := (*s)[key]
	if !ok || len(stack) == 0 {
		return nil
	}

	val := (*s)[key][len(stack)-1]
	(*s)[key] = (*s)[key][:len(stack)-1]
	return val
}
