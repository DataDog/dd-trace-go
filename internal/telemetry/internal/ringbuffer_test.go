// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package internal

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEnqueueSingleElement(t *testing.T) {
	queue := NewRingQueue[int](Range[int]{3, 5})
	success := queue.Enqueue(1)
	assert.True(t, success)
	assert.Equal(t, 1, queue.ReversePeek())
}

func TestEnqueueMultipleElements(t *testing.T) {
	queue := NewRingQueue[int](Range[int]{3, 5})
	success := queue.Enqueue(1, 2, 3)
	assert.True(t, success)
	assert.Equal(t, 3, queue.ReversePeek())
}

func TestEnqueueBeyondCapacity(t *testing.T) {
	queue := NewRingQueue[int](Range[int]{3, 5})
	success := queue.Enqueue(1, 2, 3, 4, 5, 6)
	assert.False(t, success)
	assert.Equal(t, 6, queue.ReversePeek())
}

func TestDequeueSingleElement(t *testing.T) {
	queue := NewRingQueue[int](Range[int]{3, 5})
	queue.Enqueue(1)
	val := queue.Dequeue()
	assert.Equal(t, 1, val)
}

func TestDequeueMultipleElements(t *testing.T) {
	queue := NewRingQueue[int](Range[int]{3, 5})
	queue.Enqueue(1, 2, 3)
	val1 := queue.Dequeue()
	val2 := queue.Dequeue()
	assert.Equal(t, 1, val1)
	assert.Equal(t, 2, val2)
}

func TestDequeueFromEmptyQueueAfterBeingFull(t *testing.T) {
	queue := NewRingQueue[int](Range[int]{1, 1})
	assert.True(t, queue.Enqueue(1))
	assert.False(t, queue.Enqueue(2))
	assert.Equal(t, []int{2}, queue.Flush())

	// Should return the zero value for int
	assert.Equal(t, 0, queue.Dequeue())
}

func TestDequeueFromEmptyQueue(t *testing.T) {
	queue := NewRingQueue[int](Range[int]{3, 5})
	val := queue.Dequeue()
	assert.Equal(t, 0, val) // Assuming zero value for int
}

func TestGetBufferAndReset(t *testing.T) {
	queue := NewRingQueue[int](Range[int]{3, 5})
	queue.Enqueue(1, 2, 3)
	buffer := queue.getBuffer()
	assert.Equal(t, []int{1, 2, 3}, buffer)
	assert.True(t, queue.IsEmpty())
}

func TestFlushAndReset(t *testing.T) {
	queue := NewRingQueue[int](Range[int]{3, 5})
	queue.Enqueue(1, 2, 3)
	buffer := queue.Flush()
	assert.Equal(t, []int{1, 2, 3}, buffer)
	assert.True(t, queue.IsEmpty())
}

func TestIsEmptyWhenQueueIsEmpty(t *testing.T) {
	queue := NewRingQueue[int](Range[int]{3, 5})
	assert.True(t, queue.IsEmpty())
}

func TestIsEmptyWhenQueueIsNotEmpty(t *testing.T) {
	queue := NewRingQueue[int](Range[int]{3, 5})
	queue.Enqueue(1)
	assert.False(t, queue.IsEmpty())
}

func TestIsFullWhenQueueIsFull(t *testing.T) {
	queue := NewRingQueue[int](Range[int]{3, 5})
	queue.Enqueue(1, 2, 3, 4, 5)
	assert.True(t, queue.IsFull())
}

func TestIsFullWhenQueueIsNotFull(t *testing.T) {
	queue := NewRingQueue[int](Range[int]{3, 5})
	queue.Enqueue(1, 2, 3)
	assert.False(t, queue.IsFull())
}

func TestEnqueueToFullQueue(t *testing.T) {
	queue := NewRingQueue[int](Range[int]{3, 3})
	success := queue.Enqueue(1, 2, 3)
	assert.True(t, success)
	success = queue.Enqueue(4)
	assert.False(t, success)
	assert.Equal(t, 4, queue.ReversePeek())
}

func TestDequeueFromFullQueue(t *testing.T) {
	queue := NewRingQueue[int](Range[int]{3, 3})
	queue.Enqueue(1, 2, 3)
	val := queue.Dequeue()
	assert.Equal(t, 1, val)
	assert.False(t, queue.IsFull())
}

func TestEnqueueAfterDequeue(t *testing.T) {
	queue := NewRingQueue[int](Range[int]{3, 3})
	queue.Enqueue(1, 2, 3)
	queue.Dequeue()
	success := queue.Enqueue(4)
	assert.True(t, success)
	assert.Equal(t, 4, queue.ReversePeek())
}

func TestDequeueAllElements(t *testing.T) {
	queue := NewRingQueue[int](Range[int]{3, 3})
	queue.Enqueue(1, 2, 3)
	val1 := queue.Dequeue()
	val2 := queue.Dequeue()
	val3 := queue.Dequeue()
	assert.Equal(t, 1, val1)
	assert.Equal(t, 2, val2)
	assert.Equal(t, 3, val3)
	assert.True(t, queue.IsEmpty())
}

func TestEnqueueAndDequeueInterleaved(t *testing.T) {
	queue := NewRingQueue[int](Range[int]{3, 3})
	queue.Enqueue(1)
	val1 := queue.Dequeue()
	queue.Enqueue(2)
	val2 := queue.Dequeue()
	queue.Enqueue(3, 4)
	val3 := queue.Dequeue()
	assert.Equal(t, 1, val1)
	assert.Equal(t, 2, val2)
	assert.Equal(t, 3, val3)
	assert.False(t, queue.IsEmpty())
}

func TestEnqueueWithResize(t *testing.T) {
	queue := NewRingQueue[int](Range[int]{2, 4})
	queue.Enqueue(1, 2)
	assert.Equal(t, 2, len(queue.buffer))
	queue.Enqueue(3)
	assert.Equal(t, 4, len(queue.buffer))
	assert.Equal(t, 3, queue.ReversePeek())
}
