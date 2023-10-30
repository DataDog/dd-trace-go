// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strconv"

	"github.com/DataDog/dd-trace-go/v2/ddtrace"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

//var _ ddtrace.SpanContext = (*spanContext)(nil)

type traceID [16]byte // traceID in big endian, i.e. <upper><lower>

var emptyTraceID traceID

func (t *traceID) HexEncoded() string {
	return hex.EncodeToString(t[:])
}

func (t *traceID) Lower() uint64 {
	return binary.BigEndian.Uint64(t[8:])
}

func (t *traceID) Upper() uint64 {
	return binary.BigEndian.Uint64(t[:8])
}

func (t *traceID) SetLower(i uint64) {
	binary.BigEndian.PutUint64(t[8:], i)
}

func (t *traceID) SetUpper(i uint64) {
	binary.BigEndian.PutUint64(t[:8], i)
}

func (t *traceID) SetUpperFromHex(s string) error {
	u, err := strconv.ParseUint(s, 16, 64)
	if err != nil {
		return fmt.Errorf("malformed %q: %s", s, err)
	}
	t.SetUpper(u)
	return nil
}

func (t *traceID) Empty() bool {
	return *t == emptyTraceID
}

func (t *traceID) HasUpper() bool {
	//TODO: in go 1.20 we can simplify this
	for _, b := range t[:8] {
		if b != 0 {
			return true
		}
	}
	return false
}

func (t *traceID) UpperHex() string {
	return hex.EncodeToString(t[:8])
}

// // SpanContext represents a span state that can propagate to descendant spans
// // and across process boundaries. It contains all the information needed to
// // spawn a direct descendant of the span that it belongs to. It can be used
// // to create distributed tracing by propagating it using the provided interfaces.
// type spanContext struct {
// 	updated bool // updated is tracking changes for priority / origin / x-datadog-tags
//
// 	// the below group should propagate only locally
//
// 	trace  *trace // reference to the trace that this span belongs too
// 	span   *Span  // reference to the span that hosts this context
// 	errors int32  // number of spans with errors in this trace
//
// 	// the below group should propagate cross-process
//
// 	traceID traceID
// 	spanID  uint64
//
// 	mu         sync.RWMutex // guards below fields
// 	baggage    map[string]string
// 	hasBaggage uint32 // atomic int for quick checking presence of baggage. 0 indicates no baggage, otherwise baggage exists.
// 	origin     string // e.g. "synthetics"
// }
//
// // newSpanContext creates a new SpanContext to serve as context for the given
// // span. If the provided parent is not nil, the context will inherit the trace,
// // baggage and other values from it. This method also pushes the span into the
// // new context's trace and as a result, it should not be called multiple times
// // for the same span.
// func newSpanContext(span *Span, parent *spanContext) *spanContext {
// 	context := &spanContext{
// 		spanID: span.SpanID,
// 		span:   span,
// 	}
// 	context.traceID.SetLower(span.TraceID)
// 	if parent != nil {
// 		context.traceID.SetUpper(parent.traceID.Upper())
// 		context.trace = parent.trace
// 		context.origin = parent.origin
// 		context.errors = parent.errors
// 		parent.ForeachBaggageItem(func(k, v string) bool {
// 			context.setBaggageItem(k, v)
// 			return true
// 		})
// 	} else if sharedinternal.BoolEnv("DD_TRACE_128_BIT_TRACEID_GENERATION_ENABLED", false) {
// 		// add 128 bit trace id, if enabled, formatted as big-endian:
// 		// <32-bit unix seconds> <32 bits of zero> <64 random bits>
// 		id128 := time.Duration(span.Start) / time.Second
// 		// casting from int64 -> uint32 should be safe since the start time won't be
// 		// negative, and the seconds should fit within 32-bits for the foreseeable future.
// 		// (We only want 32 bits of time, then the rest is zero)
// 		tUp := uint64(uint32(id128)) << 32 // We need the time at the upper 32 bits of the uint
// 		context.traceID.SetUpper(tUp)
// 	}
// 	if context.trace == nil {
// 		context.trace = newTrace()
// 	}
// 	if context.trace.root == nil {
// 		// first span in the trace can safely be assumed to be the root
// 		context.trace.root = span
// 	}
// 	// put span in context's trace
// 	context.trace.push(span)
// 	// setting context.updated to false here is necessary to distinguish
// 	// between initializing properties of the span (priority)
// 	// and updating them after extracting context through propagators
// 	context.updated = false
// 	return context
// }
//
// // SpanID implements ddtrace.SpanContext.
// func (c *spanContext) SpanID() uint64 { return c.spanID }
//
// // TraceID implements ddtrace.SpanContext.
// func (c *spanContext) TraceID() uint64 { return c.traceID.Lower() }
//
// // TraceID128 implements ddtrace.SpanContextW3C.
// func (c *spanContext) TraceID128() string {
// 	return c.traceID.HexEncoded()
// }
//
// // TraceID128Bytes implements ddtrace.SpanContextW3C.
// func (c *spanContext) TraceID128Bytes() [16]byte {
// 	return c.traceID
// }
//
// // ForeachBaggageItem implements ddtrace.SpanContext.
// func (c *spanContext) ForeachBaggageItem(handler func(k, v string) bool) {
// 	if atomic.LoadUint32(&c.hasBaggage) == 0 {
// 		return
// 	}
// 	c.mu.RLock()
// 	defer c.mu.RUnlock()
// 	for k, v := range c.baggage {
// 		if !handler(k, v) {
// 			break
// 		}
// 	}
// }
//
// func (c *spanContext) setSamplingPriority(p int, sampler samplernames.SamplerName) {
// 	if c.trace == nil {
// 		c.trace = newTrace()
// 	}
// 	if c.trace.priority != nil && *c.trace.priority != float64(p) {
// 		c.updated = true
// 	}
// 	c.trace.setSamplingPriority(p, sampler)
// }
//
// func (c *spanContext) samplingPriority() (p int, ok bool) {
// 	if c.trace == nil {
// 		return 0, false
// 	}
// 	return c.trace.samplingPriority()
// }
//
// func (c *spanContext) setBaggageItem(key, val string) {
// 	c.mu.Lock()
// 	defer c.mu.Unlock()
// 	if c.baggage == nil {
// 		atomic.StoreUint32(&c.hasBaggage, 1)
// 		c.baggage = make(map[string]string, 1)
// 	}
// 	c.baggage[key] = val
// }
//
// func (c *spanContext) baggageItem(key string) string {
// 	if atomic.LoadUint32(&c.hasBaggage) == 0 {
// 		return ""
// 	}
// 	c.mu.RLock()
// 	defer c.mu.RUnlock()
// 	return c.baggage[key]
// }
//
// func (c *spanContext) meta(key string) (val string, ok bool) {
// 	c.span.RLock()
// 	defer c.span.RUnlock()
// 	val, ok = c.span.Meta[key]
// 	return val, ok
// }
//
// // finish marks this span as finished in the trace.
// func (c *spanContext) finish() { c.trace.finishedOne(c.span) }

// setPeerService sets the peer.service, _dd.peer.service.source, and _dd.peer.service.remapped_from
// tags as applicable for the given span.
func setPeerService(s *ddtrace.Span, cfg *config) {
	// TODO(kjn v2): MORE setMeta!? Why always setMeta!?
	if _, ok := s.Meta[ext.PeerService]; ok { // peer.service already set on the span
		//s.setMeta(keyPeerServiceSource, ext.PeerService)
		s.SetTag(keyPeerServiceSource, ext.PeerService)
	} else { // no peer.service currently set
		spanKind := s.Meta[ext.SpanKind]
		isOutboundRequest := spanKind == ext.SpanKindClient || spanKind == ext.SpanKindProducer
		shouldSetDefaultPeerService := isOutboundRequest && cfg.peerServiceDefaultsEnabled
		if !shouldSetDefaultPeerService {
			return
		}
		source := setPeerServiceFromSource(s)
		if source == "" {
			log.Debug("No source tag value could be found for span %q, peer.service not set", s.Name)
			return
		}
		//s.setMeta(keyPeerServiceSource, source)
		s.SetTag(keyPeerServiceSource, source)
	}
	// Overwrite existing peer.service value if remapped by the user
	ps := s.Meta[ext.PeerService]
	if to, ok := cfg.peerServiceMappings[ps]; ok {
		//s.setMeta(keyPeerServiceRemappedFrom, ps)
		//s.setMeta(ext.PeerService, to)
		s.SetTag(keyPeerServiceRemappedFrom, ps)
		s.SetTag(ext.PeerService, to)
	}
}

// setPeerServiceFromSource sets peer.service from the sources determined
// by the tags on the span. It returns the source tag name that it used for
// the peer.service value, or the empty string if no valid source tag was available.
func setPeerServiceFromSource(s *ddtrace.Span) string {
	has := func(tag string) bool {
		_, ok := s.Meta[tag]
		return ok
	}
	var sources []string
	useTargetHost := true
	switch {
	// order of the cases and their sources matters here. These are in priority order (highest to lowest)
	case has("aws_service"):
		sources = []string{
			"queuename",
			"topicname",
			"streamname",
			"tablename",
			"bucketname",
		}
	case s.Meta[ext.DBSystem] == ext.DBSystemCassandra:
		sources = []string{
			ext.CassandraContactPoints,
		}
		useTargetHost = false
	case has(ext.DBSystem):
		sources = []string{
			ext.DBName,
			ext.DBInstance,
		}
	case has(ext.MessagingSystem):
		sources = []string{
			ext.KafkaBootstrapServers,
		}
	case has(ext.RPCSystem):
		sources = []string{
			ext.RPCService,
		}
	}
	// network destination tags will be used as fallback unless there are higher priority sources already set.
	if useTargetHost {
		sources = append(sources, []string{
			ext.NetworkDestinationName,
			ext.PeerHostname,
			ext.TargetHost,
		}...)
	}
	for _, source := range sources {
		if val, ok := s.Meta[source]; ok {
			// TODO(kjn v2): More setMeta
			//s.setMeta(ext.PeerService, val)
			s.SetTag(ext.PeerService, val)
			return source
		}
	}
	return ""
}
