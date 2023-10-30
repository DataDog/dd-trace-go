package ddtrace

import (
	"strconv"
	"sync"
	"sync/atomic"

	ginternal "github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/samplernames"
)

// samplingDecision is the decision to send a trace to the agent or not.
type samplingDecision uint32

const (
	// decisionNone is the default state of a trace.
	// If no decision is made about the trace, the trace won't be sent to the agent.
	decisionNone samplingDecision = iota
	// decisionDrop prevents the trace from being sent to the agent.
	decisionDrop
	// decisionKeep ensures the trace will be sent to the agent.
	decisionKeep
)

// trace contains shared context information about a trace, such as sampling
// priority, the root reference and a buffer of the spans which are part of the
// trace, if these exist.
type Trace struct {
	mu               sync.RWMutex      // guards below fields
	spans            []*Span           // all the spans that are part of this trace
	tags             map[string]string // trace level tags
	propagatingTags  map[string]string // trace level tags that will be propagated across service boundaries
	finished         int               // the number of finished spans
	full             bool              // signifies that the span buffer is full
	priority         *float64          // sampling priority
	locked           bool              // specifies if the sampling priority can be altered
	samplingDecision samplingDecision  // samplingDecision indicates whether to send the trace to the agent.

	// root specifies the root of the trace, if known; it is nil when a span
	// context is extracted from a carrier, at which point there are no spans in
	// the trace yet.
	root *Span

	// TODO(kjn v2): Perhaps we do not need this. Maybe we should not keep a reference to the tracer.
	// This is the tracer we will be submitting to?
	tracer Tracer
}

var (
	// traceStartSize is the initial size of our trace buffer,
	// by default we allocate for a handful of spans within the trace,
	// reasonable as span is actually way bigger, and avoids re-allocating
	// over and over. Could be fine-tuned at runtime.
	traceStartSize = 10

	// TODO(kjn v2): I think this is fine to export? It'd be nice if it was a const
	// and not changeable. Changing at random times might cause issues.... Needs more
	// investigation.
	//
	// traceMaxSize is the maximum number of spans we keep in memory for a
	// single trace. This is to avoid memory leaks. If more spans than this
	// are added to a trace, then the trace is dropped and the spans are
	// discarded. Adding additional spans after a trace is dropped does
	// nothing.
	TraceMaxSize = int(1e5)
)

// newTrace creates a new trace using the given callback which will be called
// upon completion of the trace.
func newTrace() *Trace {
	return &Trace{spans: make([]*Span, 0, traceStartSize)}
}

func (t *Trace) samplingPriorityLocked() (p int, ok bool) {
	if t.priority == nil {
		return 0, false
	}
	return int(*t.priority), true
}

func (t *Trace) samplingPriority() (p int, ok bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.samplingPriorityLocked()
}

func (t *Trace) setSamplingPriority(p int, sampler samplernames.SamplerName) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.setSamplingPriorityLocked(p, sampler)
}

func (t *Trace) keep() {
	atomic.CompareAndSwapUint32((*uint32)(&t.samplingDecision), uint32(decisionNone), uint32(decisionKeep))
}

func (t *Trace) drop() {
	atomic.CompareAndSwapUint32((*uint32)(&t.samplingDecision), uint32(decisionNone), uint32(decisionDrop))
}

func (t *Trace) setTag(key, value string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.setTagLocked(key, value)
}

func (t *Trace) setTagLocked(key, value string) {
	if t.tags == nil {
		t.tags = make(map[string]string, 1)
	}
	t.tags[key] = value
}

func (t *Trace) setSamplingPriorityLocked(p int, sampler samplernames.SamplerName) {
	if t.locked {
		return
	}
	if t.priority == nil {
		t.priority = new(float64)
	}
	*t.priority = float64(p)
	_, ok := t.propagatingTags[keyDecisionMaker]
	if p > 0 && !ok && sampler != samplernames.Unknown {
		// We have a positive priority and the sampling mechanism isn't set.
		// Send nothing when sampler is `Unknown` for RFC compliance.
		t.setPropagatingTagLocked(keyDecisionMaker, "-"+strconv.Itoa(int(sampler)))
	}
	if p <= 0 && ok {
		delete(t.propagatingTags, keyDecisionMaker)
	}
}

// push pushes a new span into the trace. If the buffer is full, it returns
// a errBufferFull error.
func (t *Trace) push(sp *Span) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.full {
		return
	}
	//root := t.root
	//var tr Tracer
	//if root != nil {
	//	tr = root.tracer
	//}
	if len(t.spans) >= TraceMaxSize {
		// capacity is reached, we will not be able to complete this trace.
		t.full = true
		t.spans = nil // GC
		log.Error("trace buffer full (%d), dropping trace", TraceMaxSize)
		//TODO(kjn v2): Do we still need this check?
		// We should not need a reference to the tracer in order to count metrics & stuff.
		//if tr != nil {
		//	atomic.AddUint32(&tr.tracesDropped, 1)
		//}
		return
	}
	if v, ok := sp.Metrics[keySamplingPriority]; ok {
		t.setSamplingPriorityLocked(int(v), samplernames.Unknown)
	}
	t.spans = append(t.spans, sp)
	//TODO(kjn v2): Do we still need this check?
	// We should not need a reference to the tracer in order to count metrics & stuff.
	//if tr != nil {
	//	atomic.AddUint32(&tr.spansStarted, 1)
	//}
}

// setTraceTags sets all "trace level" tags on the provided span
// t must already be locked.
func (t *Trace) setTraceTags(s *Span, tr Tracer) {
	//TODO(kjn v2): Why were these all setMeta? Does it matter?
	for k, v := range t.tags {
		s.SetTag(k, v)
	}
	for k, v := range t.propagatingTags {
		s.SetTag(k, v)
	}
	for k, v := range ginternal.GetTracerGitMetadataTags() {
		s.SetTag(k, v)
	}
	// TODO(kjn v2): Do we still need this check?
	// 	if s.context != nil && s.context.traceID.HasUpper() {
	// 		s.SetTag(keyTraceID128, s.context.traceID.UpperHex())
	// 	}
	// TODO(kjn v2): How do we do this?
	// 	if hn := tr.hostname(); hn != "" {
	// 		s.SetTag(keyTracerHostname, hn)
	// 	}
}

// TODO(kjn v2): This should disappear from the public API, and maybe disappear altogether.
type Chunk struct {
	Spans    []*Span
	WillSend bool // willSend indicates whether the trace will be sent to the agent.
}

// finishedOne acknowledges that another span in the trace has finished, and checks
// if the trace is complete, in which case it calls the onFinish function. It uses
// the given priority, if non-nil, to mark the root span. This also will trigger a partial flush
// if enabled and the total number of finished spans is greater than or equal to the partial flush limit.
// The provided span must be locked.
func (t *Trace) finishedOne(s *Span) {
	t.mu.Lock()
	defer t.mu.Unlock()
	// TODO(kjn v2): trace functions should not be modifying span state. This needs to move into a span function.
	// s.finished = true

	if t.full {
		// capacity has been reached, the buffer is no longer tracking
		// all the spans in the trace, so the below conditions will not
		// be accurate and would trigger a pre-mature flush, exposing us
		// to a race condition where spans can be modified while flushing.
		//
		// TODO(partialFlush): should we do a partial flush in this scenario?
		return
	}
	t.finished++
	tr := GetGlobalTracer()
	if tr == nil {
		return
	}

	// TODO(kjn v2): How to get config from running tracer?
	// Maybe the tracer itself should set peer service?
	// setPeerService(s, tr.config)

	// TODO(kjn v2): How to get config from running tracer?
	// 	// attach the _dd.base_service tag only when the globally configured service name is different from the
	// 	// span service name.
	// 	if s.Service != "" && !strings.EqualFold(s.Service, tr.config.serviceName) {
	// 		s.Meta[keyBaseService] = tr.config.serviceName
	// 	}
	if s == t.root && t.priority != nil {
		// after the root has finished we lock down the priority;
		// we won't be able to make changes to a span after finishing
		// without causing a race condition.
		// TODO(kjn v2): Why setMetric!?
		// t.root.setMetric(keySamplingPriority, *t.priority)
		t.root.SetTag(keySamplingPriority, *t.priority)
		t.locked = true
	}
	if len(t.spans) > 0 && s == t.spans[0] {
		// first span in chunk finished, lock down the tags
		//
		// TODO(barbayar): make sure this doesn't happen in vain when switching to
		// the new wire format. We won't need to set the tags on the first span
		// in the chunk there.
		t.setTraceTags(s, tr)
	}

	if len(t.spans) == t.finished { // perform a full flush of all spans
		t.finishChunk(tr, &Chunk{
			Spans:    t.spans,
			WillSend: decisionKeep == samplingDecision(atomic.LoadUint32((*uint32)(&t.samplingDecision))),
		})
		t.spans = nil
		return
	}

	// TODO(kjn v2): Partial flushing behavior does not belong to traces / spans. This functionality belongs
	// in the tracer.
	//
	// 	doPartialFlush := tr.config.partialFlushEnabled && t.finished >= tr.config.partialFlushMinSpans
	// 	if !doPartialFlush {
	// 		return // The trace hasn't completed and partial flushing will not occur
	// 	}
	// 	log.Debug("Partial flush triggered with %d finished spans", t.finished)
	// 	telemetry.GlobalClient.Count(telemetry.NamespaceTracers, "trace_partial_flush.count", 1, []string{"reason:large_trace"}, true)
	// 	finishedSpans := make([]*Span, 0, t.finished)
	// 	leftoverSpans := make([]*Span, 0, len(t.spans)-t.finished)
	// 	for _, s2 := range t.spans {
	// 		if s2.finished {
	// 			finishedSpans = append(finishedSpans, s2)
	// 		} else {
	// 			leftoverSpans = append(leftoverSpans, s2)
	// 		}
	// 	}
	// 	// TODO: (Support MetricKindDist) Re-enable these when we actually support `MetricKindDist`
	// 	//telemetry.GlobalClient.Record(telemetry.NamespaceTracers, telemetry.MetricKindDist, "trace_partial_flush.spans_closed", float64(len(finishedSpans)), nil, true)
	// 	//telemetry.GlobalClient.Record(telemetry.NamespaceTracers, telemetry.MetricKindDist, "trace_partial_flush.spans_remaining", float64(len(leftoverSpans)), nil, true)
	// 	finishedSpans[0].setMetric(keySamplingPriority, *t.priority)
	// 	if s != t.spans[0] {
	// 		// Make sure the first span in the chunk has the trace-level tags
	// 		t.setTraceTags(finishedSpans[0], tr)
	// 	}
	// 	t.finishChunk(tr, &chunk{
	// 		spans:    finishedSpans,
	// 		willSend: decisionKeep == samplingDecision(atomic.LoadUint32((*uint32)(&t.samplingDecision))),
	// 	})
	// 	t.spans = leftoverSpans
}

func (t *Trace) finishChunk(tr Tracer, ch *Chunk) {
	// TODO(kjn v2): More stats counting. This is not the way.
	// atomic.AddUint32(&tr.spansFinished, uint32(len(ch.spans)))
	tr.PushChunk(ch)
	t.finished = 0 // important, because a buffer can be used for several flushes
}
