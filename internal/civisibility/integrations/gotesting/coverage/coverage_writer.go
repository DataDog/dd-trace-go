// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package coverage

import (
	"sync"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/net"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	telemetrylog "github.com/DataDog/dd-trace-go/v2/internal/telemetry/log"
)

// Constants defining the payload size limits for agentless mode.
const (
	// agentlessPayloadMaxLimit is the maximum payload size allowed, indicating the
	// maximum size of the package that the intake can receive.
	agentlessPayloadMaxLimit = 50 * 1024 * 1024 // 5 MB

	// agentlessPayloadSizeLimit specifies the maximum allowed size of the payload before
	// it triggers a flush to the transport.
	agentlessPayloadSizeLimit = agentlessPayloadMaxLimit / 2

	// concurrentConnectionLimit specifies the maximum number of concurrent outgoing
	// connections allowed.
	concurrentConnectionLimit = 100
)

type coverageWriter struct {
	client                    net.Client       // http client
	payload                   *coveragePayload // Encodes and buffers events in msgpack format.
	climit                    chan struct{}    // Limits the number of concurrent outgoing connections.
	wg                        sync.WaitGroup   // Waits for all uploads to finish.
	mu                        sync.Mutex       // Guards payload rotation and pendingSerializationError.
	pendingSerializationError error            // First serialization error in the active payload window.
}

func reportCoverageEncodingError(err error) {
	telemetrylog.ReportError("coverageWriter: Error encoding msgpack", err)
}

func reportCoverageBufferError(err error) {
	telemetrylog.LogAndReportError("coverageWriter: failure getting coverage data", err)
}

func newCoverageWriter() *coverageWriter {
	log.Debug("coverageWriter: creating trace writer instance")
	return &coverageWriter{
		client:  net.NewClientForCodeCoverage(),
		payload: newCoveragePayload(),
		climit:  make(chan struct{}, concurrentConnectionLimit),
	}
}

func (w *coverageWriter) add(coverage *testCoverage) {
	telemetry.EventsEnqueueForSerialization()
	ciTestCoverage := newCiTestCoverageData(coverage)
	var (
		payloadToFlush     *coveragePayload
		serializationError error
	)

	w.mu.Lock()
	if err := w.payload.push(ciTestCoverage); err != nil {
		w.recordSerializationErrorLocked(err)
	}
	if w.payload.size() > agentlessPayloadSizeLimit {
		payloadToFlush = w.rotatePayloadLocked()
		serializationError = w.takePendingSerializationErrorLocked()
	}
	w.mu.Unlock()

	if serializationError != nil {
		reportCoverageEncodingError(serializationError)
	}
	if payloadToFlush != nil {
		w.flushPayload(payloadToFlush)
	}
}

func (w *coverageWriter) stop() {
	log.Debug("coverageWriter: stopping writer")
	w.flush()
	w.wg.Wait()
	if closer, ok := w.client.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

func (w *coverageWriter) flush() {
	w.mu.Lock()
	payloadToFlush := w.rotatePayloadLocked()
	serializationError := w.takePendingSerializationErrorLocked()
	w.mu.Unlock()

	if serializationError != nil {
		reportCoverageEncodingError(serializationError)
	}
	if payloadToFlush != nil {
		w.flushPayload(payloadToFlush)
	}
}

// recordSerializationErrorLocked preserves the local error log and retains
// one representative for Error Tracking at the next payload boundary.
// w.mu must be held by the caller.
func (w *coverageWriter) recordSerializationErrorLocked(err error) {
	log.Error("coverageWriter: Error encoding msgpack: %s", err.Error())
	if w.pendingSerializationError == nil {
		w.pendingSerializationError = err
	}
}

// takePendingSerializationErrorLocked returns and clears the pending Error
// Tracking report for the current payload window. w.mu must be held by the caller.
func (w *coverageWriter) takePendingSerializationErrorLocked() error {
	err := w.pendingSerializationError
	w.pendingSerializationError = nil
	return err
}

// rotatePayloadLocked swaps out the current payload while w.mu is held.
func (w *coverageWriter) rotatePayloadLocked() *coveragePayload {
	if w.payload.itemCount() == 0 {
		return nil
	}

	oldp := w.payload
	w.payload = newCoveragePayload()
	return oldp
}

// flushPayload sends a closed payload asynchronously without holding w.mu.
func (w *coverageWriter) flushPayload(oldp *coveragePayload) {
	w.wg.Add(1)
	w.climit <- struct{}{}
	go func(p *coveragePayload) {
		defer func() {
			// Once the payload has been used, clear the buffer for garbage
			// collection to avoid a memory leak when references to this object
			// may still be kept by faulty transport implementations or the
			// standard library. See dd-trace-go#976
			p.clear()

			<-w.climit
			w.wg.Done()
		}()

		size, count := p.size(), p.itemCount()
		log.Debug("coverageWriter: sending payload: size: %d events: %d\n", size, count)

		buf, err := p.getBuffer()
		if err != nil {
			reportCoverageBufferError(err)
			return
		}

		telemetry.CodeCoverageFiles(float64(p.itemCount()))
		err = w.client.SendCoveragePayload(buf)
		if err != nil {
			log.Error("coverageWriter: failure sending coverage data: %s", err.Error()) //errtrack:ignore remote request failure
		}
	}(oldp)
}
