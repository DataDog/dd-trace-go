// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadog.com/).
// Copyright 2018 Datadog, Inc.

package tracer

import (
	"bytes"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/tinylib/msgp/msgp"
)

const (
	// testFlushInterval is the flush interval that will be used for the
	// duration of the tests.
	testFlushInterval = 24 * time.Hour

	// testFlushThreshold is the flush threshold that will be used for the
	// duration of the tests.
	testFlushThreshold = 1e3

	// testInChannelSize is the input channel's buffer size that will be used
	// for the duration of the tests.
	testInChannelSize = 1000
)

func TestMain(m *testing.M) {
	o1, o2, o3 := flushInterval, flushThreshold, inChannelSize
	flushInterval = testFlushInterval
	flushThreshold = testFlushThreshold
	inChannelSize = testInChannelSize

	defer func() {
		flushInterval, flushThreshold, inChannelSize = o1, o2, o3
	}()

	os.Exit(m.Run())
}

func TestTraceExporter(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}

	t.Run("threshold", func(t *testing.T) {
		assert := assert.New(t)
		me := newTestTraceExporter(t)
		defer me.Flush()
		span := newBasicSpan("basic.span")
		size := span.Msgsize()
		n := 3
		for added := 0; added < flushThreshold; added += size {
			me.exportSpan(span)
			n++
		}

		assert.Len(me.payloads(), 0)

		// go overboard
		me.exportSpan(span)
		me.exportSpan(span)
		me.exportSpan(span)

		time.Sleep(2 * time.Millisecond) // wait for recv
		me.wg.Wait()                     // wait for flush
		flushed := me.payloads()
		assert.Len(flushed, 1)
		assert.Len(flushed[0], n)
	})

	t.Run("stop", func(t *testing.T) {
		me := newTestTraceExporter(t)
		me.exportSpan(newBasicSpan("basic.span"))

		time.Sleep(time.Millisecond) // wait for recv

		me.Flush()
		if len(me.payloads()) != 1 {
			t.Fatalf("expected to flush 1, got %d", len(me.payloads()))
		}
	})
}

// testTraceExporter wraps a defaultExporter, recording all flushed payloads.
type testTraceExporter struct {
	*defaultExporter
	t *testing.T

	mu      sync.RWMutex
	flushed []spanList
}

func newTestTraceExporter(t *testing.T) *testTraceExporter {
	me := &testTraceExporter{
		defaultExporter: newDefaultExporter("").(*defaultExporter),
		flushed:         make([]spanList, 0),
	}
	me.defaultExporter.uploadFn = me.uploadFn
	return me
}

// payloads returns all payloads that were uploaded by this exporter.
func (me *testTraceExporter) payloads() []spanList {
	me.mu.RLock()
	defer me.mu.RUnlock()
	return me.flushed
}

func (me *testTraceExporter) uploadFn(buf *bytes.Buffer, _ uint64) error {
	var ddp spanList
	if err := msgp.Decode(buf, &ddp); err != nil {
		me.t.Fatal(err)
	}
	me.mu.Lock()
	me.flushed = append(me.flushed, ddp)
	me.mu.Unlock()
	return nil
}
