// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package connectshared

import (
	"net/http"
	"strconv"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/runtime/protoimpl"
)

// notModifiedStatus is the http.status_code of a not-modified response; like other HTTP
// integrations, it is a string.
var notModifiedStatus = strconv.Itoa(http.StatusNotModified)

// FinishMode selects which outcomes a finishing span treats as non-errors, and which code it
// reports. Only UnaryFinishMode makes a not-modified error a 304 response.
type FinishMode struct {
	// AllowEOF makes connect's end-of-stream signal a non-error.
	AllowEOF bool
	// Wire reports a handler's error or panic with the code that the server transports send for
	// it.
	Wire bool
	// HTTP adds to Wire the rule of connect's HTTP server transports: they send an uncoded error that
	// wraps os.ErrDeadlineExceeded, such as a network timeout, as CodeDeadlineExceeded.
	HTTP bool
	// allowNotModified makes a not-modified error a 304 response rather than an error.
	allowNotModified bool
}

// UnaryFinishMode returns the finish mode of a unary call span over protocol whose request used
// httpMethod, which is empty if no HTTP request carried the call: only a Connect GET may be
// answered with a 304. wire is set on servers, which with an httpMethod also apply the rule of
// connect's HTTP server transports (see FinishMode.HTTP).
func UnaryFinishMode(protocol *Protocol, httpMethod string, wire bool) FinishMode {
	return FinishMode{
		allowNotModified: protocol.notModifiedEligible && httpMethod == http.MethodGet,
		Wire:             wire,
		HTTP:             wire && httpMethod != "",
	}
}

func (m FinishMode) codeOf(lib *Library, err error) Code {
	if m.Wire {
		return lib.wireCodeOf(err, m.HTTP)
	}
	return lib.codeOf(err)
}

// panicCode is codeOf for a panic with err. On servers it is CodeInternal, the code of a call that
// its handler's panic fails: connectinprocess recovers the panic with that code, and HTTP/2 resets
// the stream with INTERNAL_ERROR.
func (m FinishMode) panicCode(lib *Library, err error) Code {
	if m.Wire {
		return CodeInternal
	}
	return lib.codeOf(err)
}

// SpanFinish describes how a span is classified when it finishes.
type SpanFinish struct {
	// Procedure is passed to the error check.
	Procedure string
	// Protocol selects the status and error detail tags. Nil is the unknown protocol, as
	// ProtocolFor("") returns it.
	Protocol *Protocol
	// Mode selects the non-error outcomes and the reported code.
	Mode FinishMode
}

type finishOutcome struct {
	code        Code
	expectedEOF bool
	notModified bool
	recorded    error // the error to record on the span, if any
}

// finishOutcomeFor classifies a normalized, non-panic err.
func (c *Config) finishOutcomeFor(err error, procedure string, mode FinishMode) finishOutcome {
	if err == nil {
		return finishOutcome{}
	}
	out := finishOutcome{
		code:        mode.codeOf(c.lib, err),
		expectedEOF: mode.AllowEOF && c.lib.isExpectedStreamEOF(err),
		notModified: mode.allowNotModified && c.lib.isNotModified(err),
	}
	switch {
	case out.expectedEOF, out.notModified, c.lib.isUncodedCancellation(err), c.nonErrorCodes[out.code]:
	case c.errCheck != nil && !c.errCheck(procedure, err):
	default:
		out.recorded = err
	}
	return out
}

// isSuppressedTerminalError applies only the code-based rules of a call span finished with mode to
// a normalized err, never the error check, so client streams can compare terminal error candidates
// without calling it.
func (c *Config) isSuppressedTerminalError(err error, mode FinishMode) bool {
	return err == nil || (mode.allowNotModified && c.lib.isNotModified(err)) ||
		c.lib.isUncodedCancellation(err) || c.nonErrorCodes[mode.codeOf(c.lib, err)]
}

// IsExpectedStreamEOF reports whether err is connect's normal end-of-stream signal. err must be
// safe to inspect, as the errors that FinishOnReturn passes to its after function are.
func (c *Config) IsExpectedStreamEOF(err error) bool {
	return c.lib.isExpectedStreamEOF(err)
}

// IsRemoteError reports whether err is a peer's verdict: a *connect.Error that a transport decoded
// from the response, or took from the handler of an in-process call. An error that is unsafe to
// inspect is not.
func (c *Config) IsRemoteError(err error) bool {
	return err != nil && c.lib.isSafeToInspect(err) && c.lib.inspect(err).Remote
}

// FinishSpan finishes span, unless it is nil, with err as f classifies it. A panic in the error
// check finishes the span with the panic, which is then re-raised. The span itself finishes outside
// of any recovery: the tracer formats err while it holds the span's lock, so a panic there, from an
// error method that panics only when called again, leaves the span locked and must not reach code
// that would finish it again, such as FinishOnOpenPanic.
func (c *Config) FinishSpan(span *tracer.Span, err error, f SpanFinish) {
	if span == nil {
		return
	}
	err = c.lib.normalizeError(err)
	var recorded error
	if panicked, value := recoverPanic(
		func() { recorded = c.tagOutcome(span, err, c.finishOutcomeFor(err, f.Procedure, f.Mode), f.Protocol) },
		func(value any) { c.finishSpan(span, c.lib.panicError(value), f, true) },
	); panicked {
		panic(value)
	}
	// Outside of recoverPanic, whose frames would be in the stack that the span records.
	c.endSpan(span, recorded)
}

// finishSpan finishes span with err. A panic is always recorded, even when its value would
// otherwise be suppressed, because it is re-raised and must stay visible in the trace.
func (c *Config) finishSpan(span *tracer.Span, err error, f SpanFinish, isPanic bool) {
	if span == nil {
		return
	}
	err = c.lib.normalizeError(err)
	var out finishOutcome
	if isPanic {
		out = finishOutcome{code: f.Mode.panicCode(c.lib, err), recorded: err}
	} else {
		out = c.finishOutcomeFor(err, f.Procedure, f.Mode)
	}
	c.endSpan(span, c.tagOutcome(span, err, out, f.Protocol))
}

// tagOutcome sets the tags that out gives a non-nil span, and returns the error that the span
// records, if any. Status tags reflect err even when out suppresses it.
func (c *Config) tagOutcome(span *tracer.Span, err error, out finishOutcome, protocol *Protocol) error {
	if protocol == nil {
		protocol = &unknownProtocol
	}
	failed := err != nil && !out.expectedEOF
	switch {
	case protocol.grpcStatus:
		var code uint32
		if failed {
			code = uint32(out.code)
		}
		span.SetTag(protocol.statusCodeTag, code)
	case failed && !out.notModified && protocol.statusCodeTag != "":
		span.SetTag(protocol.statusCodeTag, out.code.String())
	}
	if out.notModified {
		span.SetTag(ext.HTTPCode, notModifiedStatus)
	}
	if out.recorded == nil {
		return nil
	}
	if c.withErrorDetailTags {
		c.setErrorDetailTags(span, out.recorded, protocol)
	}
	return recordableError(out.recorded)
}

// endSpan finishes span, recording err unless it is nil.
func (c *Config) endSpan(span *tracer.Span, err error) {
	switch {
	case err == nil:
		span.Finish()
	case c.noDebugStack:
		span.Finish(tracer.WithError(err), tracer.NoDebugStack())
	default:
		span.Finish(tracer.WithError(err))
	}
}

// setErrorDetailTags tags span with the details of the *connect.Error in err's chain, by their
// index.
func (c *Config) setErrorDetailTags(span *tracer.Span, err error, protocol *Protocol) {
	if c.lib.ErrorDetails == nil {
		return
	}
	c.lib.ErrorDetails(err, ErrorDetailTags{span: span, protocol: protocol})
}

// ErrorDetailTags tags the span that records an error with the error's details. It is a value
// rather than a function so that passing it to Library.ErrorDetails costs no allocation.
type ErrorDetailTags struct {
	span     *tracer.Span
	protocol *Protocol
}

// set tags the span with detail, the error's detail at index.
func (t ErrorDetailTags) set(index int, detail proto.Message) {
	t.span.SetTag(t.protocol.statusDetailsPrefix+"_"+strconv.Itoa(index), protoimpl.X.MessageStringOf(detail))
}

// TagErrorDetails tags the span of tags with each detail in details that decode can decode, under
// its index in details. It skips a nil detail, keeping the indexes of the others: connect v1 keeps
// one where AddDetail is given nil or a Connect error body holds null.
func TagErrorDetails[D any](tags ErrorDetailTags, details []*D, decode func(*D) (proto.Message, error)) {
	for i, detail := range details {
		if detail == nil {
			continue
		}
		if message, err := decode(detail); err == nil {
			tags.set(i, message)
		}
	}
}

// FinishOnReturn finishes *span with *err as *f classifies it, then runs after, if set, with the
// normalized error. It must be deferred by a function that runs its traced code with guard.Run, and
// reads through pointers because the traced code may start the span and set *f: connect v1's unary
// client only knows the HTTP method that selects its finish mode once the request has been sent.
// A panic of the traced code, including a panic(nil) with GODEBUG=panicnil=1, still finishes the
// span and runs after while the panic is in flight. So does a panic in the error check, which is
// then re-raised. If the traced code calls runtime.Goexit, the span finishes as on a return.
func (c *Config) FinishOnReturn(guard *Guard, span **tracer.Span, err *error, f *SpanFinish, after func(err error, isPanic bool)) {
	if guard.panicked {
		panicErr := c.lib.panicError(guard.value)
		c.finishSpan(*span, panicErr, *f, true)
		if after != nil {
			after(panicErr, true)
		}
		return
	}
	opErr := c.lib.normalizeError(*err)
	if *span != nil {
		var recorded error
		if panicked, value := recoverPanic(
			func() {
				recorded = c.tagOutcome(*span, opErr, c.finishOutcomeFor(opErr, f.Procedure, f.Mode), f.Protocol)
			},
			func(value any) { c.finishSpan(*span, c.lib.panicError(value), *f, true) },
		); panicked {
			if after != nil {
				after(c.lib.panicError(value), true)
			}
			panic(value)
		}
		// Outside of recoverPanic, whose frames would be in the stack that the span records.
		c.endSpan(*span, recorded)
	}
	if after != nil {
		after(opErr, false)
	}
}

// FinishOnOpenPanic finishes *span with the panic of the code that guard ran to open the stream of
// a call to procedure, if it panicked. It must be deferred by the function that runs that code with
// guard.Run, which re-raises the panic. protocol is only called then, and returns the protocol to
// tag the status with.
func (c *Config) FinishOnOpenPanic(guard *Guard, span **tracer.Span, protocol func() *Protocol, procedure string) {
	if !guard.panicked {
		return
	}
	f := SpanFinish{Procedure: procedure, Protocol: protocol()}
	c.finishSpan(*span, c.lib.panicError(guard.value), f, true)
}

// finishTerminal finishes a client stream's call span with its terminal error err as f classifies
// it or, while the error check rejects the error chosen so far, with each of fallbacks in arrival
// order. A panic, as err or from the error check, is always recorded; one from the error check is
// returned for the caller to re-raise, so that the runtime reports it as a new panic rather than
// as a recovered and re-raised one.
func (c *Config) finishTerminal(span *tracer.Span, err error, isPanic bool, fallbacks []error, f SpanFinish) (panicked bool, value any) {
	if isPanic {
		c.finishSpan(span, err, f, true)
		return false, nil
	}
	var recorded error
	if panicked, value = recoverPanic(
		func() {
			chosen, out := c.classifyTerminal(err, fallbacks, f)
			recorded = c.tagOutcome(span, chosen, out, f.Protocol)
		},
		func(value any) { c.finishSpan(span, c.lib.panicError(value), f, true) },
	); panicked {
		return true, value
	}
	// Outside of recoverPanic, whose frames would be in the stack that the span records.
	c.endSpan(span, recorded)
	return false, nil
}

// classifyTerminal classifies err and, while the error check rejects the chosen error, each
// fallback in arrival order.
func (c *Config) classifyTerminal(err error, fallbacks []error, f SpanFinish) (error, finishOutcome) {
	err = c.lib.normalizeError(err)
	out := c.finishOutcomeFor(err, f.Procedure, f.Mode)
	for _, fallback := range fallbacks {
		if out.recorded != nil {
			break
		}
		fallback = c.lib.normalizeError(fallback)
		if fallbackOut := c.finishOutcomeFor(fallback, f.Procedure, f.Mode); fallbackOut.recorded != nil {
			err, out = fallback, fallbackOut
		}
	}
	return err, out
}
