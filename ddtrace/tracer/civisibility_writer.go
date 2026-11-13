// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package tracer

import (
	"errors"
	"io"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	telemetrycommon "github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	telemetrylog "github.com/DataDog/dd-trace-go/v2/internal/telemetry/log"
)

// Constants defining the payload size limits for agentless mode.
const (
	// agentlessPayloadMaxLimit is the maximum payload size allowed, indicating the
	// maximum size of the package that the intake can receive.
	agentlessPayloadMaxLimit = 5 * 1024 * 1024 // 5 MB

	// agentlessPayloadSizeLimit specifies the maximum allowed size of the payload before
	// it triggers a flush to the transport.
	agentlessPayloadSizeLimit = agentlessPayloadMaxLimit / 2
)

// Ensure that ciVisibilityTraceWriter implements the traceWriter interface.
var _ traceWriter = (*ciVisibilityTraceWriter)(nil)

// ciVisibilityTraceWriter is responsible for buffering and sending CI visibility trace data
// to the Datadog backend. It manages the payload size and flushes the data when necessary.
type ciVisibilityTraceWriter struct {
	config  *config              // Configuration for the tracer.
	payload *ciVisibilityPayload // Encodes and buffers events in msgpack format.
	climit  chan struct{}        // Limits the number of concurrent outgoing connections.
	wg      sync.WaitGroup       // Waits for all uploads to finish.

	// encodeFailures counts per-event msgpack encode failures and encodeErr
	// holds the first one. add() runs on the per-event path, where a
	// ReportError would capture a stack trace for every failed event, so the
	// failures are aggregated here and reported once at a flush or stop
	// boundary — see reportEncodeFailures.
	encodeFailures atomic.Uint64
	encodeErr      atomic.Pointer[error]
	encodeReported atomic.Bool
}

// newCiVisibilityTraceWriter creates a new instance of ciVisibilityTraceWriter.
//
// Parameters:
//
//	c - The tracer configuration.
//
// Returns:
//
//	A pointer to an initialized ciVisibilityTraceWriter.
func newCiVisibilityTraceWriter(c *config) *ciVisibilityTraceWriter {
	log.Debug("ciVisibilityTraceWriter: creating trace writer instance")
	return &ciVisibilityTraceWriter{
		config:  c,
		payload: newCiVisibilityPayload(),
		climit:  make(chan struct{}, concurrentConnectionLimit),
	}
}

// add adds a new trace to the payload. If the payload size exceeds the limit,
// it triggers a flush to send the data.
//
// Parameters:
//
//	trace - A slice of spans representing the trace to be added.
func (w *ciVisibilityTraceWriter) add(trace []*Span) {
	telemetry.EventsEnqueueForSerialization()
	for _, s := range trace {
		cvEvent := getCiVisibilityEvent(s)
		size, err := w.payload.push(cvEvent)
		if err != nil {
			w.encodeFailures.Add(1)
			// firstErr keeps err addressable only on this branch. Taking &err
			// above the branch instead would make every event's err escape to the
			// heap, even when the encode succeeds.
			firstErr := err
			w.encodeErr.CompareAndSwap(nil, &firstErr)
			log.Error("ciVisibilityTraceWriter: Error encoding msgpack: %s", err.Error()) //errtrack:ignore per-event path; aggregated by reportEncodeFailures at the flush boundary
		}
		if size > agentlessPayloadSizeLimit {
			w.flush()
		}
	}
}

// stop stops the trace writer, ensuring all data is flushed and all uploads are completed.
func (w *ciVisibilityTraceWriter) stop() {
	w.flush()
	w.wg.Wait()
	if transport, ok := w.config.ddTransport.(*ciVisibilityTransport); ok {
		transport.httpClient.CloseIdleConnections()
	}
}

func (w *ciVisibilityTraceWriter) wait() { w.wg.Wait() }

// flush sends the current payload to the transport. It ensures that the payload is reset
// and the resources are freed after the flush operation is completed.
func (w *ciVisibilityTraceWriter) flush() {
	// Report before the empty-payload early return: a writer whose every event
	// failed to encode holds zero items and must still surface the defect.
	w.reportEncodeFailures()
	if w.payload.stats().itemCount == 0 {
		return
	}

	w.wg.Add(1)
	w.climit <- struct{}{}
	oldp := w.payload
	w.payload = newCiVisibilityPayload()

	go func(p *ciVisibilityPayload) {
		defer func(_ time.Time) {
			// Once the payload has been used, clear the buffer for garbage
			// collection to avoid a memory leak when references to this object
			// may still be kept by faulty transport implementations or the
			// standard library. See dd-trace-go#976
			p.clear()

			<-w.climit
			w.wg.Done()
		}(time.Now())

		var count, size int
		var err error

		requestCompressedType := telemetry.UncompressedRequestCompressedType
		if ciTransport, ok := w.config.ddTransport.(*ciVisibilityTransport); ok && ciTransport.agentless {
			requestCompressedType = telemetry.CompressedRequestCompressedType
		}
		telemetry.EndpointPayloadRequests(telemetry.TestCycleEndpointType, requestCompressedType)

		sendRetries := w.config.internalConfig.SendRetries()
		retryInterval := w.config.internalConfig.RetryInterval()
		for attempt := 0; attempt <= sendRetries; attempt++ {
			stats := p.stats()
			size, count = stats.size, stats.itemCount
			log.Debug("ciVisibilityTraceWriter: sending payload: size: %d events: %d\n", size, count)
			var body io.ReadCloser
			body, err = w.config.ddTransport.send(p.payload)
			if body != nil {
				closeCIVisibilityResponseBody(body)
			}
			if err == nil {
				log.Debug("ciVisibilityTraceWriter: sent events after %d attempts", attempt+1)
				return
			}
			log.Error("ciVisibilityTraceWriter: failure sending events (attempt %d of %d): %v", attempt+1, sendRetries+1, err.Error()) //errtrack:ignore payload, agent, or network failure; the final drop below reports payload construction errors
			p.reset()
			time.Sleep(retryInterval)
		}
		reportCIVisibilitySendError(err, count)
		telemetry.EndpointPayloadDropped(telemetry.TestCycleEndpointType)
	}(oldp)
}

// reportCIVisibilitySendError handles the final swallow point of the flush
// goroutine, where a payload that exhausted its send attempts is dropped for
// good. A payload construction failure (ciVisibilityPayloadError from
// ciVisibilityTransport.send) is an SDK serialization defect, so it is
// reported to Error Tracking; agent, network, and filesystem failures are
// customer-environment conditions and stay local-only — the dropped count is
// already surfaced through telemetry.EndpointPayloadDropped.
func reportCIVisibilitySendError(err error, count int) {
	if payloadErr, ok := errors.AsType[*ciVisibilityPayloadError](err); ok {
		log.Error("ciVisibilityTraceWriter: lost %d events: %v", count, err.Error()) //errtrack:ignore reported by ReportError below
		telemetrylog.ReportError("Error building CI Visibility payload", payloadErr.err)
	} else {
		log.Error("ciVisibilityTraceWriter: lost %d events: %v", count, err.Error()) //errtrack:ignore agent or network failure already reported as a count
	}
}

// reportEncodeFailures reports the aggregated per-event msgpack encode
// failure once per writer lifetime, at a flush or stop boundary. A ReportError
// in add() would capture a stack trace for every failed event; the aggregate
// keeps the Error Tracking count per process instead of per event.
func (w *ciVisibilityTraceWriter) reportEncodeFailures() {
	if w.encodeReported.Load() {
		return
	}
	count := w.encodeFailures.Load()
	err := w.encodeErr.Load()
	if count == 0 || err == nil {
		return
	}
	if w.encodeReported.CompareAndSwap(false, true) {
		telemetrylog.ReportError(
			"Error encoding CI Visibility events",
			*err,
			telemetrycommon.WithTags([]string{"events:" + strconv.FormatUint(count, 10)}),
		)
	}
}

// closeCIVisibilityResponseBody drains and closes a body returned by ddTransport.send.
// The CI Visibility writer owns returned response bodies and must release them
// before shutdown so net/http can close or reuse the underlying connection.
func closeCIVisibilityResponseBody(body io.ReadCloser) {
	_, _ = io.Copy(io.Discard, body)
	_ = body.Close()
}
