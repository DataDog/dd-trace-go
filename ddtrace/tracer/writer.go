// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	globalinternal "github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/globalconfig"
	"github.com/DataDog/dd-trace-go/v2/internal/locking"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/version"
)

type traceWriter interface {
	// add adds traces to be sent by the writer.
	add([]*Span)

	// flush causes the writer to send any buffered traces.
	flush()

	// stop gracefully shuts down the writer.
	stop()
}

type agentTraceWriter struct {
	// config holds the tracer configuration
	config *config

	// mu synchronizes access to payload operations
	mu locking.Mutex

	// payload encodes and buffers traces in msgpack format
	payload payload // +checklocks:mu

	// climit limits the number of concurrent outgoing connections
	climit chan struct{}

	// wg waits for all uploads to finish
	wg sync.WaitGroup

	// prioritySampling is the prioritySampler into which agentTraceWriter will
	// read sampling rates sent by the agent
	prioritySampling *prioritySampler

	// statsd is used to send metrics
	statsd globalinternal.StatsdClient

	tracesQueued uint32 // +checkatomic
}

func newAgentTraceWriter(c *config, s *prioritySampler, statsdClient globalinternal.StatsdClient) *agentTraceWriter {
	tw := &agentTraceWriter{
		config:           c,
		climit:           make(chan struct{}, concurrentConnectionLimit),
		prioritySampling: s,
		statsd:           statsdClient,
	}
	tw.payload = tw.newPayload(c.effectiveTraceProtocol(), 0)
	return tw
}

// add pushes trace into h.payload without checking whether h.payload's
// protocol still matches effectiveTraceProtocol(). That check (plus sealing a
// non-empty mismatched payload for immediate async send) was added by #5167
// and reverted here (issue #5258 diagnostic): direct benchmarking found it
// added no measurable steady-state cost, but the sealed-payload path itself
// cost 6-12x a normal add() call when a transition was actually in flight,
// and #5167's own re-evaluation machinery (protocolState, rotateStalePayload,
// effectiveTraceProtocol) is left fully intact — flush() still reads it fresh
// on every call and unconditionally rebuilds h.payload for the current
// protocol regardless of what oldp's protocol was, so a real protocol change
// still gets picked up correctly, just at the next flush instead of
// immediately. The trade-off: a payload that is mid-buffering when a
// transition happens can keep absorbing spans (which will go out under
// whatever protocol h.payload started with) until that next flush, instead
// of being sealed and resent immediately. A transition that lands on an
// already-in-flight v1.0 payload after the agent has genuinely stopped
// accepting it is not silently corrupted — the existing errV1TracesNotSupported
// handling in sendAsync (downgradeAfterRejectedSend) still drops and reports
// it via reason:v1_rejected. See the two TestEmptyPayload.../
// TestNonEmptyPayload... tests in trace_protocol_runtime_test.go for the
// exact contract this leaves, and datadog.tracer.trace_protocol_changed for
// the production frequency data (issue #5258) that motivated this trade-off.
func (h *agentTraceWriter) add(trace []*Span) {
	h.mu.Lock()
	stats, err := h.payload.push(trace)
	if err != nil {
		h.mu.Unlock()
		h.statsd.Incr("datadog.tracer.traces_dropped", []string{"reason:encoding_error"}, 1)
		log.Error("Error encoding msgpack: %s", err.Error())
		return
	}
	// TODO: This does not differentiate between complete traces and partial chunks
	atomic.AddUint32(&h.tracesQueued, 1)

	needsFlush := stats.size > payloadSizeLimit
	h.mu.Unlock()

	if needsFlush {
		h.statsd.Incr("datadog.tracer.flush_triggered", []string{"reason:size"}, 1)
		h.flush()
	}
}

func (h *agentTraceWriter) stop() {
	h.statsd.Incr("datadog.tracer.flush_triggered", []string{"reason:shutdown"}, 1)
	h.flush()
	h.wg.Wait()
}

// newPayload returns a new payload for protocol. hint, when positive,
// pre-sizes the buffer to the previous flush cycle's actual encoded size,
// eliminating the doubling ramp-up at the cost of one upfront allocation.
// The hint is a lagging heuristic: under-prediction falls back to organic growth;
// over-prediction wastes one cycle of transient memory and self-corrects. Pass 0
// on cold start (no prior cycle data).
func (h *agentTraceWriter) newPayload(protocol float64, hint int) payload {
	payload := newPayload(protocol)
	if payload.protocol() == traceProtocolV04 {
		if hint > 0 {
			payload.grow(hint)
		}
		return payload
	}
	// pre-allocate payloadV1 with field values
	pv1 := payload.(*safePayload).p.(*payloadV1)
	pv1.SetLanguageName("go")
	pv1.SetLanguageVersion(runtime.Version())
	pv1.SetTracerVersion(version.Tag)
	pv1.SetRuntimeID(globalconfig.RuntimeID())
	if v := h.config.internalConfig.Env(); v != "" {
		pv1.SetEnv(v)
	}
	if v := h.config.internalConfig.Hostname(); v != "" {
		pv1.SetHostname(v)
	}
	if v := h.config.internalConfig.Version(); v != "" {
		pv1.SetAppVersion(v)
	}
	if cid := globalinternal.ContainerID(); cid != "" {
		pv1.SetContainerID(cid)
	}
	if hint > 0 {
		pv1.sizeHint = hint
	}

	return payload
}

// rotateStalePayload replaces h.payload with a freshly protocol-matched one if
// it is still empty but was built for a protocol the agent no longer (or not
// yet) accepts. An empty payload holds no encoded bytes, so it can adopt the
// current protocol for free. Both add and flush must call this before relying
// on h.payload's protocol: add is the first point a trace can land in an idle
// payload, and flush is the only place that otherwise re-reads the protocol —
// without either call, a writer that saw no traffic across an agent-info poll
// would hold a stale payload indefinitely after the agent's advertised
// protocol changed. A non-empty stale payload is not this function's job: add
// seals that case itself (in the same critical section, so there is no gap
// between the mismatch check and the push) since a non-empty payload cannot
// switch protocol for free. The discarded payload was never handed to the
// transport, so it is simply dropped rather than recycled through the
// payloadV1 pool:
// handoff(pv1StateFlushDone) alone would not return it (the two-party handoff
// also needs the transport's bit), and forcing both bits risks a double pool
// return.
// +checklocks:h.mu
func (h *agentTraceWriter) rotateStalePayload(protocol float64) {
	if h.payload.itemCount() == 0 && h.payload.protocol() != protocol {
		h.payload = h.newPayload(protocol, 0)
	}
}

// flush will push any currently buffered traces to the server.
func (h *agentTraceWriter) flush() {
	h.mu.Lock()
	// Read under the lock, same reasoning as add(): a read taken before
	// h.mu.Lock() could go stale relative to a concurrent add()/flush() that
	// raced ahead and already updated h.payload for a newer protocol.
	protocol := h.config.effectiveTraceProtocol()
	oldp := h.payload
	// Check after acquiring lock
	if oldp.itemCount() == 0 {
		h.rotateStalePayload(protocol)
		h.mu.Unlock()
		return
	}
	h.payload = h.newPayload(protocol, min(oldp.size(), int(payloadMaxLimit)))
	h.mu.Unlock()

	h.sendAsync(oldp)
}

// retirePayload runs the cleanup a payload needs once it's done being sent:
// v1 payloads hand off to their pool via the two-party bit, v0.4 payloads are
// just cleared. Shared by sendAsync's deferred cleanup and by
// downgradeAfterRejectedSend's caller, which retires the original v1 payload
// early — before that deferred cleanup runs — because it replaces it with a
// re-encoded v0.4 payload for the rest of the send loop.
func (h *agentTraceWriter) retirePayload(p payload) {
	if p.protocol() == traceProtocolV1 {
		p.(*safePayload).p.(*payloadV1).handoff(pv1StateFlushDone)
	} else {
		p.clear()
	}
}

// sendAsync spawns a goroutine that sends p to the agent and retires it
// through the same lifecycle as a normal flush. Callers must not hold h.mu:
// acquiring h.climit can block on the concurrent-connection limit, and doing
// that while holding h.mu would stall every other add()/flush() call system-
// wide, not just the ones actually contending for a connection slot.
func (h *agentTraceWriter) sendAsync(p payload) {
	h.climit <- struct{}{}
	h.wg.Add(1)
	go func(p payload) {
		defer func(start time.Time) {
			// Once the payload has been used, clear the buffer for garbage
			// collection to avoid a memory leak when references to this object
			// may still be kept by faulty transport implementations or the
			// standard library. See dd-trace-go#976
			h.statsd.Count("datadog.tracer.queue.enqueued.traces", int64(atomic.SwapUint32(&h.tracesQueued, 0)), nil, 1)
			// Must branch on p.protocol(), never on config: the effective protocol
			// can change between the newPayload call that created p and this
			// deferred cleanup (e.g. an agent-info poll landing mid-flush), so only
			// the payload's own, immutable protocol is a reliable guide here.
			h.retirePayload(p)
			<-h.climit
			h.statsd.Timing("datadog.tracer.flush_duration", time.Since(start), nil, 1)
			h.wg.Done()
		}(time.Now())

		stats := p.stats()
		var err error
		sendRetries := h.config.internalConfig.SendRetries()
		retryInterval := h.config.internalConfig.RetryInterval()
		for attempt := 0; attempt <= sendRetries; attempt++ {
			log.Debug("Attempt to send payload: size: %d traces: %d\n", stats.size, stats.itemCount)
			var rc io.ReadCloser
			rc, err = h.config.ddTransport.send(p)
			if err == nil {
				log.Debug("sent traces after %d attempts", attempt+1)
				h.statsd.Count("datadog.tracer.flush_bytes", int64(stats.size), nil, 1)
				h.statsd.Count("datadog.tracer.flush_traces", int64(stats.itemCount), nil, 1)
				if err := h.prioritySampling.readRatesJSON(rc); err != nil {
					h.statsd.Incr("datadog.tracer.decode_error", nil, 1)
				}
				return
			}

			if errors.Is(err, errV1TracesNotSupported) {
				// Authoritative: this specific backend just rejected v1 outright,
				// unlike an /info poll, which only samples one backend behind a
				// possibly load-balanced address. Downgrade immediately rather than
				// retrying the same bytes against an endpoint that will keep
				// rejecting them; the payload is dropped rather than redelivered.
				// Under concurrent flush traffic, every other payload already
				// committed to v1 (in flight or queued behind h.climit) independently
				// discovers the same rejection and drops itself the same way, so a
				// real rollback can cost up to concurrentConnectionLimit payloads,
				// not just this one (see doc.go).
				h.downgradeAfterRejectedSend()
				h.statsd.Count("datadog.tracer.traces_dropped", int64(stats.itemCount), []string{"reason:v1_rejected"}, 1)
				log.Error("agent rejected a v1 trace payload; dropping %d traces and downgrading to v0.4", stats.itemCount)
				return
			}

			if (attempt+1)%5 == 0 {
				log.Error("failure sending traces (attempt %d of %d): %v", attempt+1, sendRetries+1, err.Error())
			}
			p.reset()
			time.Sleep(retryInterval)
		}
		h.statsd.Count("datadog.tracer.traces_dropped", int64(stats.itemCount), []string{"reason:send_failed"}, 1)
		log.Error("lost %d traces: %v", stats.itemCount, err.Error())
	}(p)
}

// downgradeAfterRejectedSend records that an agent backend just rejected a v1
// send outright — conclusive evidence, on the same footing as a negative
// /info poll (see (*tracer).refreshAgentFeatures). advanceTraceProtocolState
// is monotone, so this can only move the state to protoV04 and never back.
//
// The log/metric below are gated on ReportEffectiveTraceProtocol's own return
// value, not on advanceTraceProtocolState's: those are two separate,
// independently-racing CAS operations (protocolState vs
// effectiveTraceProtocolBits), so a caller winning the first is no guarantee
// it also wins the second. A concurrent /info poll observing the same
// transition can win ReportEffectiveTraceProtocol first; gating on the wrong
// CAS would then double-emit for one logical transition. This mirrors
// refreshAgentFeatures's tail exactly. One accepted trade-off: since emission
// now follows whichever caller wins the shared telemetry dedup, a genuine
// rejection can occasionally lose its "reason:send_rejected" tag to a
// concurrent poll's plainer report — the transition is still only ever
// reported once, just not always attributed to this path.
func (h *agentTraceWriter) downgradeAfterRejectedSend() {
	h.config.advanceTraceProtocolState(protoV04)
	// This registers the downgrade (state, telemetry, log) only -- it does not
	// also rotate h.payload. An earlier version of this diagnostic build
	// (issue #5258) added a compensating rotation here so the very next trace
	// wouldn't land in a payload built moments ago for the still-believed-good
	// v1 protocol, but Codex review on #5263 correctly flagged that the
	// rotation and this state flip aren't atomic with add(): a concurrent
	// add() can populate that payload between the two, making it non-empty --
	// rotateStalePayload only replaces an empty one -- so the compensating
	// rotation could silently do nothing under exactly the concurrent traffic
	// it was meant to help with. Fixing that race properly means serializing
	// this with add() under h.mu, which reintroduces the per-add() cost this
	// revert exists to remove. Simpler and consistent with removing add()'s
	// own check entirely: don't special-case this path either. flush()'s
	// existing, unconditional rebuild-for-current-protocol on every call is
	// the only recovery mechanism now, same as any other post-transition
	// staleness this diagnostic build accepts -- see add()'s doc comment.
	if !h.config.internalConfig.ReportEffectiveTraceProtocol(h.config.effectiveTraceProtocol()) {
		return
	}
	h.statsd.Incr("datadog.tracer.trace_protocol_changed", []string{"to:0.4", "reason:send_rejected"}, 1)
	log.Info("agent rejected a v1 trace payload; downgrading to v0.4")
}

// logWriter specifies the output target of the logTraceWriter; replaced in tests.
var logWriter io.Writer = os.Stdout

// logTraceWriter encodes traces into a format understood by the Datadog Forwarder
// (https://github.com/DataDog/datadog-serverless-functions/tree/master/aws/logs_monitoring)
// and writes them to os.Stdout. This is used to send traces from an AWS Lambda environment.
type logTraceWriter struct {
	config    *config
	buf       bytes.Buffer
	hasTraces bool
	w         io.Writer
	statsd    globalinternal.StatsdClient
}

func newLogTraceWriter(c *config, statsdClient globalinternal.StatsdClient) *logTraceWriter {
	w := &logTraceWriter{
		config: c,
		w:      logWriter,
		statsd: statsdClient,
	}
	w.resetBuffer()
	return w
}

const (
	// maxFloatLength is the maximum length that a string encoded by encodeFloat will be.
	maxFloatLength = 24

	// logBufferSuffix is the final string that the trace writer has to append to a buffer to close
	// the JSON.
	logBufferSuffix = "]}\n"

	// logBufferLimit is the maximum size log line allowed by cloudwatch
	logBufferLimit = 256 * 1024
)

func (h *logTraceWriter) resetBuffer() {
	h.buf.Reset()
	h.buf.WriteString(`{"traces": [`)
	h.hasTraces = false
}

// encodeFloat correctly encodes float64 into the JSON format followed by ES6.
// This code is reworked from Go's encoding/json package
// (https://github.com/golang/go/blob/go1.15/src/encoding/json/encode.go#L573)
//
// One important departure from encoding/json is that infinities and nans are encoded
// as null rather than signalling an error.
func encodeFloat(p []byte, f float64) []byte {
	if math.IsInf(f, 0) || math.IsNaN(f) {
		return append(p, "null"...)
	}
	abs := math.Abs(f)
	if abs != 0 && (abs < 1e-6 || abs >= 1e21) {
		p = strconv.AppendFloat(p, f, 'e', -1, 64)
		// clean up e-09 to e-9
		n := len(p)
		if n >= 4 && p[n-4] == 'e' && p[n-3] == '-' && p[n-2] == '0' {
			p[n-2] = p[n-1]
			p = p[:n-1]
		}
	} else {
		p = strconv.AppendFloat(p, f, 'f', -1, 64)
	}
	return p
}

// +checklocksignore — Post-finish: serializes finished span for log transport.
func (h *logTraceWriter) encodeSpan(s *Span) {
	var scratch [maxFloatLength]byte
	h.buf.WriteString(`{"trace_id":"`)
	h.buf.Write(strconv.AppendUint(scratch[:0], uint64(s.traceID), 16))
	h.buf.WriteString(`","span_id":"`)
	h.buf.Write(strconv.AppendUint(scratch[:0], uint64(s.spanID), 16))
	h.buf.WriteString(`","parent_id":"`)
	h.buf.Write(strconv.AppendUint(scratch[:0], uint64(s.parentID), 16))
	h.buf.WriteString(`","name":`)
	h.marshalString(s.name)
	h.buf.WriteString(`,"resource":`)
	h.marshalString(s.resource)
	h.buf.WriteString(`,"error":`)
	h.buf.Write(strconv.AppendInt(scratch[:0], int64(s.error), 10))
	h.buf.WriteString(`,"meta":{`)
	first := true
	for k, v := range s.meta.All() {
		if first {
			first = false
		} else {
			h.buf.WriteString(",")
		}
		h.marshalString(k)
		h.buf.WriteString(":")
		h.marshalString(v)
	}
	// We cannot pack messagepack into JSON, so we need to marshal the meta struct as JSON, and send them through the `meta` field
	for k, v := range s.metaStruct {
		if first {
			first = false
		} else {
			h.buf.WriteString(`,`)
		}
		h.marshalString(k)
		h.buf.WriteString(":")
		jsonValue, err := json.Marshal(v)
		if err != nil {
			log.Error("Error marshaling value %q: %v", v, err.Error())
			continue
		}
		h.marshalString(string(jsonValue))
	}
	h.buf.WriteString(`},"metrics":{`)
	first = true
	for k, v := range s.metrics {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			// The trace forwarder does not support infinity or nan, so we do not send metrics with those values.
			continue
		}
		if first {
			first = false
		} else {
			h.buf.WriteString(`,`)
		}
		h.marshalString(k)
		h.buf.WriteString(`:`)
		h.buf.Write(encodeFloat(scratch[:0], v))
	}
	h.buf.WriteString(`},"start":`)
	h.buf.Write(strconv.AppendInt(scratch[:0], s.start, 10))
	h.buf.WriteString(`,"duration":`)
	h.buf.Write(strconv.AppendInt(scratch[:0], s.duration, 10))
	h.buf.WriteString(`,"service":`)
	h.marshalString(s.service)
	h.buf.WriteString(`}`)
}

// marshalString marshals the string str as JSON into the writer's buffer.
// Should be used whenever writing non-constant string data to ensure correct sanitization.
func (h *logTraceWriter) marshalString(str string) {
	m, err := json.Marshal(str)
	if err != nil {
		log.Error("Error marshaling value %q: %v", str, err.Error())
	} else {
		h.buf.Write(m)
	}
}

type encodingError struct {
	cause      error
	dropReason string
}

// writeTrace makes an effort to write the trace into the current buffer. It returns
// the number of spans (n) that it wrote and an error (err), if one occurred.
// n may be less than len(trace), meaning that only the first n spans of the trace
// fit into the current buffer. Once the buffer is flushed, the remaining spans
// from the trace can be retried.
// An error, if one is returned, indicates that a span in the trace is too large
// to fit in one buffer, and the trace cannot be written.
func (h *logTraceWriter) writeTrace(trace []*Span) (n int, err *encodingError) {
	startn := h.buf.Len()
	if !h.hasTraces {
		h.buf.WriteByte('[')
	} else {
		h.buf.WriteString(", [")
	}
	written := 0
	for i, s := range trace {
		n := h.buf.Len()
		if i > 0 {
			h.buf.WriteByte(',')
		}
		h.encodeSpan(s)
		if h.buf.Len() > logBufferLimit-len(logBufferSuffix) {
			// This span is too big to fit in the current buffer.
			if i == 0 {
				// This was the first span in this trace. This means we should truncate
				// everything we wrote in writeTrace
				h.buf.Truncate(startn)
				if !h.hasTraces {
					// This is the first span of the first trace in the buffer and it's too big.
					// We will never be able to send this trace, so we will drop it.
					return 0, &encodingError{cause: errors.New("span too large for buffer"), dropReason: "trace_too_large"}
				}
				return 0, nil
			}
			// This span was too big, but it might fit in the next buffer.
			// We can finish this trace and try again with an empty buffer (see *logTaceWriter.add)
			h.buf.Truncate(n)
			break
		}
		written++
	}
	h.buf.WriteByte(']')
	h.hasTraces = true
	return written, nil
}

// add adds a trace to the writer's buffer.
func (h *logTraceWriter) add(trace []*Span) {
	// Try adding traces to the buffer until we flush them all or encounter an error.
	for len(trace) > 0 {
		n, err := h.writeTrace(trace)
		if err != nil {
			log.Error("Lost a trace: %s", err.cause)
			h.statsd.Count("datadog.tracer.traces_dropped", 1, []string{"reason:" + err.dropReason}, 1)
			return
		}
		trace = trace[n:]
		// If there are traces left that didn't fit into the buffer, flush the buffer and loop to
		// write the remaining spans.
		if len(trace) > 0 {
			h.flush()
		}
	}
}

func (h *logTraceWriter) stop() {
	h.statsd.Incr("datadog.tracer.flush_triggered", []string{"reason:shutdown"}, 1)
	h.flush()
}

// flush will write any buffered traces to standard output.
func (h *logTraceWriter) flush() {
	if !h.hasTraces {
		return
	}
	h.buf.WriteString(logBufferSuffix)
	h.w.Write(h.buf.Bytes())
	h.resetBuffer()
}
