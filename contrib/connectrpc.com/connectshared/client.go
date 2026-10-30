// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package connectshared

import (
	"context"
	"maps"
	"sync"
	"sync/atomic"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

// ClientCall finishes the call span of a client stream once the stream has reached a terminal
// state (or its context is done) and no operation is in flight, and tags the request metadata once
// per stream. A stream wrapper holds it in a field, and calls Init before any other method. Every
// operation of the stream calls Begin, then End once it returns.
type ClientCall struct {
	cfg *Config
	// span is nil without a call span; the stream then does no call-span bookkeeping.
	span   *tracer.Span
	finish SpanFinish

	// metadataRead is set by the first write-side operation, the only one that reads the metadata.
	metadataRead atomic.Bool
	// pendingMetadata holds, without a call span, a copy of the metadata for the next message span
	// when the operation that read it had none.
	pendingMetadata atomic.Pointer[map[string][]string]
	// ctx is the stream's context. It and stopContextDone are nil without a context watcher.
	ctx             context.Context
	stopContextDone func() bool
	// watcherDone is done once the context watcher has returned.
	watcherDone sync.WaitGroup

	mu            sync.Mutex
	active        int
	finishPending bool
	// responseEnded is set once an operation has returned the clean end of the response.
	responseEnded bool
	// contextCandidate holds the error of the context's end while operations are in flight, as one
	// of them may still return the clean end of the response. It is recorded before any later
	// candidate, or once the finish is claimed. Its err is nil if there is none.
	contextCandidate terminalCandidate
	// finishClaimed is set, under mu, by the one caller that finishes the call span.
	finishClaimed atomic.Bool
	terminal      terminalCandidate
	fallbacks     []error
	// result, if set, is the error that the call span finishes with (see SetResult).
	result error
}

// terminalCandidate is an error that may end the stream, classified outside of mu because
// classification runs user-defined error methods.
type terminalCandidate struct {
	err        error
	isPanic    bool
	suppressed bool
}

// Init prepares s for a stream whose call span is span, or nil without one. f classifies the
// stream's terminal error when the span finishes. The end of ctx is a terminal state: an
// interceptor or caller may abandon a stream without closing it.
func (s *ClientCall) Init(ctx context.Context, cfg *Config, span *tracer.Span, f SpanFinish) {
	s.cfg, s.span, s.finish = cfg, span, f
	// A watcher of a context that is never done would only cost allocations.
	if span != nil && ctx.Done() != nil {
		s.ctx = ctx
		s.watcherDone.Add(1)
		s.stopContextDone = context.AfterFunc(ctx, s.contextDone)
	}
}

// Begin starts an operation and reports whether the stream is still traced. Once the call span's
// finish has been claimed, its trace may have been flushed and recycled by the span pool, so it
// must not be tagged nor parent message spans.
func (s *ClientCall) Begin() (traced bool) {
	if s.span == nil {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active++
	return !s.finishClaimed.Load()
}

// End ends an operation started by Begin. err and isPanic are what FinishOnReturn passes to its
// after function for the operation; terminal reports whether the operation ended the stream, which
// a panic always does, and keepEOF whether connect's end-of-stream signal is then an error.
func (s *ClientCall) End(err error, isPanic, terminal, keepEOF bool) {
	if s.end(err, isPanic, terminal, keepEOF, false) {
		s.finishCall()
	}
}

// EndReceive ends a Receive started by Begin, like End. Any error ends the stream. With single,
// the Receive reads the only response of a single-response stream: it ends the stream, and
// connect's end-of-stream signal is then an error. A Receive that returns the end of the stream,
// or with single the response, returns the clean end of the response: the caller has the RPC's
// result, so the end of the context while it ran is not recorded.
func (s *ClientCall) EndReceive(err error, isPanic, single bool) {
	if s.span == nil {
		return
	}
	var ended bool
	switch {
	case isPanic:
	case single:
		ended = err == nil
	default:
		ended = s.cfg.lib.isExpectedStreamEOF(err)
	}
	if s.end(err, isPanic, single || err != nil, single, ended) {
		s.finishCall()
	}
}

// SetResult makes err the error that the call span finishes with, whatever error or end of the
// context ends the stream, unless an operation panics; a nil err undoes it. A stream wrapper calls
// it from an operation started by Begin, before the operation ends, once the caller's result no
// longer depends on how the stream ends.
func (s *ClientCall) SetResult(err error) {
	s.mu.Lock()
	s.result = err
	s.mu.Unlock()
}

// end ends an operation; ended reports that it returned the clean end of the response. It reports
// whether the caller must finish the call span: the caller does, so that end is not on the stack
// that the span records with an error, whose frames cost allocations.
func (s *ClientCall) end(err error, isPanic, terminal, keepEOF, ended bool) (finish bool) {
	if s.span == nil {
		return false
	}
	terminal = terminal || isPanic
	var candidate terminalCandidate
	var record bool
	// The clean end of the response is no error.
	if terminal && !ended && !s.finishClaimed.Load() {
		candidate, record = s.newTerminalCandidate(err, isPanic, keepEOF)
	}
	s.mu.Lock()
	if ended {
		s.responseEnded = true
		s.contextCandidate = terminalCandidate{}
	}
	if terminal {
		s.finishPending = true
		if record {
			s.recordContextCandidate()
			s.recordTerminal(candidate)
		}
	}
	s.active--
	// active can reach 0 more than once, and errCheck may reenter the stream from finish: only the
	// caller that claims the finish stops the context watcher.
	claimed := s.finishPending && s.active == 0 && s.finishClaimed.CompareAndSwap(false, true)
	if claimed {
		s.recordContextCandidate()
	}
	s.mu.Unlock()
	if !claimed {
		return false
	}
	if s.stopContextDone != nil && !s.stopContextDone() {
		// The watcher is running or has run: wait until it is done with the terminal state.
		s.watcherDone.Wait()
	}
	return true
}

// contextDone is the context watcher.
func (s *ClientCall) contextDone() {
	defer s.watcherDone.Done()
	s.requestFinish(s.ctx.Err())
}

// requestFinish ends the stream with err, the error of its context. It runs in the context
// watcher's own goroutine, so it must not panic.
func (s *ClientCall) requestFinish(err error) {
	recoverPanic(func() {
		candidate, record := s.newTerminalCandidate(s.cfg.lib.normalizeError(err), false, false)
		s.mu.Lock()
		s.finishPending = true
		switch {
		case !record || s.responseEnded:
			// Nothing to record, or the caller already has the RPC's result.
		case s.active > 0:
			// An operation in flight may still return the clean end of the response.
			s.contextCandidate = candidate
		default:
			s.recordTerminal(candidate)
		}
		claimed := s.active == 0 && s.finishClaimed.CompareAndSwap(false, true)
		s.mu.Unlock()
		if claimed {
			s.finishCall()
		}
	}, func(value any) {
		// Errors are aggregated by their format, so the prefix is part of it rather than an
		// argument: each integration's errors are reported separately.
		s.cfg.instr.Logger().Error(s.cfg.logPrefix+": recovered panic finishing stream after context cancellation: %v", value)
	})
}

// newTerminalCandidate reports whether err ends the stream with an error, and classifies it.
func (s *ClientCall) newTerminalCandidate(err error, isPanic, keepEOF bool) (terminalCandidate, bool) {
	if err == nil || (!isPanic && !keepEOF && s.cfg.lib.isExpectedStreamEOF(err)) {
		return terminalCandidate{}, false
	}
	return terminalCandidate{
		err:        err,
		isPanic:    isPanic,
		suppressed: !isPanic && s.cfg.isSuppressedTerminalError(err, s.finish.Mode),
	}, true
}

// recordTerminal must be called with s.mu held. A panic wins and is never displaced. Otherwise a
// suppressed error is replaced, and later unsuppressed errors are kept, in order, as fallbacks in
// case errCheck rejects the first one.
func (s *ClientCall) recordTerminal(candidate terminalCandidate) {
	switch {
	case s.terminal.err == nil:
		s.terminal = candidate
	case candidate.isPanic:
		s.terminal = candidate
		s.fallbacks = nil
	case s.terminal.isPanic:
		// Keep the panic.
	case s.terminal.suppressed:
		s.terminal = candidate
	case !candidate.suppressed:
		s.fallbacks = append(s.fallbacks, candidate.err)
	}
}

// recordContextCandidate records the error of the context's end that operations in flight held
// back, if any. It must be called with s.mu held.
func (s *ClientCall) recordContextCandidate() {
	if s.contextCandidate.err != nil {
		s.recordTerminal(s.contextCandidate)
		s.contextCandidate = terminalCandidate{}
	}
}

// finishCall finishes the call span; the caller must have claimed it. errCheck runs here, outside
// of any lock, and may reenter the stream.
func (s *ClientCall) finishCall() {
	s.mu.Lock()
	terminal, fallbacks := s.terminal, s.fallbacks
	if s.result != nil && !terminal.isPanic {
		terminal, fallbacks = terminalCandidate{err: s.result}, nil
	}
	s.mu.Unlock()
	if panicked, value := s.cfg.finishTerminal(s.span, terminal.err, terminal.isPanic, fallbacks, s.finish); panicked {
		panic(value)
	}
}

// ClaimMetadata is called by every write-side operation of the stream with its message span, if
// any, and Begin's result: only the first one reads the request metadata, before connect sends it,
// as Receive may run concurrently with the application setting it. claimed reports whether this is
// that operation. It then tags span with the metadata: the call span, or messageSpan without one.
// span is nil once the call span's finish has been claimed, or when keep is set because there is
// neither: the operation then passes a copy of the metadata to KeepMetadata, and the next
// operation with a message span tags it with that copy. The caller tags span itself, with
// SetMetadataTags: ranging here over a header iterator that the compiler doesn't know would
// allocate.
func (s *ClientCall) ClaimMetadata(messageSpan *tracer.Span, traced bool) (claimed bool, span *tracer.Span, keep bool) {
	if !s.cfg.withMetadataTags {
		return false, nil, false
	}
	if !s.metadataRead.CompareAndSwap(false, true) {
		s.TagPendingMetadata(messageSpan)
		return false, nil, false
	}
	switch {
	case s.span != nil:
		if traced {
			return true, s.span, false
		}
		return true, nil, false
	case messageSpan != nil:
		return true, messageSpan, false
	default:
		return true, nil, true
	}
}

// KeepMetadata keeps snapshot, a copy of the request metadata that ClaimMetadata asked to keep,
// for the next message span.
func (s *ClientCall) KeepMetadata(snapshot map[string][]string) {
	s.pendingMetadata.Store(&snapshot)
}

// TagPendingMetadata tags span, unless it is nil, with the metadata that KeepMetadata kept, unless
// another span already has it.
func (s *ClientCall) TagPendingMetadata(span *tracer.Span) {
	if span == nil || !s.cfg.withMetadataTags {
		return
	}
	if snapshot := s.pendingMetadata.Swap(nil); snapshot != nil {
		setMetadataTags(span, maps.All(*snapshot), s.cfg.ignoredMetadata)
	}
}
