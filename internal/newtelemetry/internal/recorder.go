// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package internal

import (
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

// Recorder is a generic thread-safe type that records functions that could have taken place before object T was created.
// Once object T is created, the Recorder can replay all the recorded functions with object T as an argument.
type Recorder[T any] struct {
	queue *RingQueue[func(T)]
}

// NewRecorder creates a new [Recorder] instance. with 512 as the maximum number of recorded functions.
func NewRecorder[T any]() Recorder[T] {
	return Recorder[T]{
		// TODO: tweak this value once we get telemetry data from the telemetry client
		queue: NewRingQueue[func(T)](16, 512),
	}
}

func (r Recorder[T]) Record(f func(T)) {
	if r.queue == nil {
		return
	}
	if !r.queue.Enqueue(f) {
		log.Debug("telemetry: recorder queue is full, dropping record")
	}
}

func (r Recorder[T]) Replay(t T) {
	if r.queue == nil {
		return
	}
	for {
		f := r.queue.Dequeue()
		if f == nil {
			break
		}
		f(t)
	}
}
