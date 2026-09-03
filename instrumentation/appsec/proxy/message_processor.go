// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package proxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"

	"github.com/DataDog/dd-trace-go/v2/appsec"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/dyngo"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/waf/actions"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/httptrace"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/body"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/body/json"
)

// Processor is a state machine that handles incoming HTTP request and response in a streaming manner,
// made for proxy external-processing protocols like Envoy's External Processing or HAProxy's SPOP.
//
// Valid state transitions (RequestState.State):
//
//	0 (initial)
//	  → RequestHeaders    OnRequestHeaders called
//
//	RequestHeaders
//	  → RequestBody       request has a body (EOS=false, supported Content-Type)
//	  → ResponseHeaders   EOS or body not supported (skip body phase)
//
//	RequestBody           one or more OnRequestBody calls until EOS
//	  → ResponseHeaders   EOS received (body fully consumed)
//
//	ResponseHeaders
//	  → ResponseBody      response has a body (EOS=false, supported Content-Type)
//	  → Finished          EOS or body not supported (stream ends here)
//
//	ResponseBody          one or more OnResponseBody calls
//	  → Finished          EOS received (body fully consumed), or the analysis finished
//	                      early and the gateway does not require the remaining body
//	                      messages to be acknowledged
//
//	Any ongoing state
//	  → Blocked           dyngo blocking callback fired inside Close/afterHandle
//
//	Finished / Blocked    terminal — no further transitions
type Processor struct {
	ProcessorConfig
	instr *instrumentation.Instrumentation

	metrics                      *metrics
	done                         context.CancelFunc
	firstRequest                 sync.Once
	computedBodyParsingSizeLimit atomic.Int64
}

// NewProcessor creates a new [Processor] instance with the given configuration and instrumentation
// It also initializes the metrics reporter and a context cancellation function
func NewProcessor(config ProcessorConfig, instr *instrumentation.Instrumentation) Processor {
	if config.Context == nil {
		config.Context = context.Background()
	}
	var done context.CancelFunc
	config.Context, done = context.WithCancel(config.Context)
	return Processor{
		ProcessorConfig: config,
		instr:           instr,
		metrics:         newMetricsReporter(config.Context, instr.Logger()),
		done:            done,
	}
}

// OnRequestHeaders handles incoming request headers using the [RequestHeaders] interface
// It returns a [RequestState] to be used in subsequent calls for the same request/response cycle
// along with an optional output message of type O created by either [ProcessorConfig.ContinueMessageFunc] or [ProcessorConfig.BlockMessageFunc]
// If the request is blocked or the message ends the stream, it returns io.EOF as error
func (mp *Processor) OnRequestHeaders(ctx context.Context, req RequestHeaders) (reqState RequestState, err error) {
	mp.metrics.incrementRequestCount()
	pseudoRequest, err := req.ExtractRequest(ctx)
	if err != nil {
		return reqState, fmt.Errorf("error extracting request header from input message: %w", err)
	}

	httpRequest, err := pseudoRequest.toNetHTTP(ctx)
	if err != nil {
		return reqState, fmt.Errorf("error converting to net/http request: %w", err)
	}

	mp.firstRequest.Do(func() {
		var bodyLimit int64
		if mp.BodyParsingSizeLimit != nil {
			bodyLimit = int64(*mp.BodyParsingSizeLimit)
		} else {
			bodyLimit = int64(req.BodyParsingSizeLimit(ctx))
		}
		mp.computedBodyParsingSizeLimit.Store(bodyLimit)

		if bodyLimit <= 0 {
			mp.instr.Logger().Info("external_processing: body parsing size limit set to 0 or negative. The request and response bodies will NOT be analyzed.")
		}
		RegisterConfig(mp)
		mp.instr.Logger().Info("external_processing: first request received. Configuration: BlockingUnavailable=%v, BodyParsingSizeLimit=%dB, Framework=%s", mp.BlockingUnavailable, mp.computedBodyParsingSizeLimit.Load(), mp.Framework)
	})

	reqState, blocked := newRequestState(
		httpRequest,
		pseudoRequest.ClientIP,
		int(mp.computedBodyParsingSizeLimit.Load()),
		mp.Framework,
		// Resolved per request rather than cached: the gateway is identified from the request
		// headers, so a single processor can serve several kinds of gateway.
		ackBodyMessagesUntilEndOfStream(ctx, req),
		req.SpanOptions(ctx)...,
	)

	defer func() {
		if err != nil {
			reqState.CloseBeforeResponse()
		}
	}()

	if !mp.BlockingUnavailable && blocked {
		actionOpts := reqState.BlockAction()
		if err := mp.BlockMessageFunc(reqState.Context, actionOpts); err != nil {
			return reqState, fmt.Errorf("error creating block message: %w", err)
		}
		return reqState, io.EOF
	}

	headerMutation, err := reqState.PropagationHeaders()
	if err != nil {
		return reqState, err
	}

	if !req.GetEndOfStream() && mp.isBodySupported(httpRequest.Header.Get("Content-Type")) {
		reqState.State = MessageTypeRequestBody
	}

	if err := mp.ContinueMessageFunc(reqState.Context, ContinueActionOptions{
		HeaderMutations: headerMutation,
		Body:            reqState.State == MessageTypeRequestBody,
		MessageType:     MessageTypeRequestHeaders,
	}); err != nil {
		return reqState, fmt.Errorf("error creating continue message: %w", err)
	}

	return reqState, nil
}

// OnRequestBody handles incoming request body chunks using the [HTTPBody] interface
// It uses the provided [RequestState] to keep track of the request/response cycle state
// It returns an optional output message of type O created by either [ProcessorConfig.ContinueMessageFunc] or [ProcessorConfig.BlockMessageFunc]
// If the request is blocked or the message ends the stream, it returns io.EOF as error
// Once the whole body has been received, it will try to parse it following the Content-Type header
// and if the body is not too large, it will be analyzed by the WAF
func (mp *Processor) OnRequestBody(req HTTPBody, reqState *RequestState) error {
	reqState.Mu.Lock()
	defer reqState.Mu.Unlock()

	if !reqState.State.Ongoing() {
		return fmt.Errorf("received request body in unexpected state: %v", reqState.State)
	}

	mp.instr.Logger().Debug("message_processor: received request body: %v - EOS: %v\n", len(req.GetBody()), req.GetEndOfStream())

	if mp.computedBodyParsingSizeLimit.Load() <= 0 || reqState.State != MessageTypeRequestBody {
		mp.instr.Logger().Error("message_processor: the body parsing has been wrongly configured. " +
			"Please refer to the official documentation for guidance on the proper settings or contact support.")

		return mp.ContinueMessageFunc(reqState.Context, ContinueActionOptions{MessageType: MessageTypeRequestBody})
	}

	blocked := processBody(reqState.Context, reqState.requestBuffer, req.GetBody(), req.GetEndOfStream(), appsec.MonitorParsedHTTPBody, "request")
	if reqState.requestBuffer.analyzed {
		// The WAF analysis is synchronous, so the retained prefix is no longer needed.
		reqState.requestBuffer.buffer = nil
	}
	if blocked != nil && !mp.BlockingUnavailable {
		mp.instr.Logger().Debug("external_processing: request blocked, end the stream")
		actionOpts := reqState.blockActionLocked()
		if err := mp.BlockMessageFunc(reqState.Context, actionOpts); err != nil {
			return fmt.Errorf("error creating block message: %w", err)
		}
		return io.EOF
	}

	return mp.ContinueMessageFunc(reqState.Context, ContinueActionOptions{MessageType: MessageTypeRequestBody})
}

// OnResponseHeaders handles incoming response headers using the [ResponseHeaders] interface
// It returns a [RequestState] to be used in subsequent calls for the same request/response cycle
// along with an optional output message of type O created by either [ProcessorConfig.ContinueMessageFunc] or [ProcessorConfig.BlockMessageFunc]
// If the request is blocked or the message ends the stream, it returns io.EOF as error
func (mp *Processor) OnResponseHeaders(res ResponseHeaders, reqState *RequestState) error {
	reqState.Mu.Lock()
	defer reqState.Mu.Unlock()

	if !reqState.State.Request() {
		return fmt.Errorf("received response headers too early: %v", reqState.State)
	}

	pseudoResponse, err := res.ExtractResponse()
	if err != nil {
		return fmt.Errorf("error extracting response header from input message: %w", err)
	}

	pseudoResponse.toNetHTTP(reqState.wrappedResponseWriter)

	// We need to know if the request has been blocked, but we don't have any other way than to look for the operation and bind a blocking data listener to it
	if !mp.BlockingUnavailable {
		op, ok := dyngo.FromContext(reqState.Context)
		if ok {
			dyngo.OnData(op, func(_ *actions.BlockHTTP) {
				// We already wrote over the response writer, we need to reset it so the blocking Processor can write to it
				httptrace.ResetStatusCode(reqState.wrappedResponseWriter)
				reqState.fakeResponseWriter.Reset()
				reqState.State = MessageTypeBlocked
			})
		}
	}

	reqState.State = MessageTypeResponseBody

	// Run the waf on the response headers only when we are sure to not receive a response body
	if res.GetEndOfStream() || !mp.isBodySupported(reqState.wrappedResponseWriter.Header().Get("Content-Type")) {
		_ = reqState.closeLocked()
		if !mp.BlockingUnavailable && reqState.State == MessageTypeBlocked {
			if err := mp.BlockMessageFunc(reqState.Context, reqState.blockActionLocked()); err != nil {
				return fmt.Errorf("error creating block message: %w", err)
			}
			return io.EOF
		}

		mp.instr.Logger().Debug("message_processor: finishing request with status code: %v\n", reqState.fakeResponseWriter.status)
		// No body message is in flight, so the ext_proc stream can close after response headers.
		return io.EOF
	}

	return mp.ContinueMessageFunc(reqState.Context, ContinueActionOptions{MessageType: MessageTypeResponseHeaders, Body: reqState.State == MessageTypeResponseBody})
}

// OnResponseBody handles incoming response body chunks using the [HTTPBody] interface
// It uses the provided [RequestState] to keep track of the request/response cycle state
// It returns an optional output message of type O created by either [ProcessorConfig.ContinueMessageFunc] or [ProcessorConfig.BlockMessageFunc]
// If the request is blocked or the message ends the stream, it returns io.EOF as error
// Once the whole body has been received, it will try to parse it following the Content-Type header
// and if the body is not too large, it will be analyzed by the WAF
func (mp *Processor) OnResponseBody(resp HTTPBody, reqState *RequestState) error {
	reqState.Mu.Lock()
	defer reqState.Mu.Unlock()

	if !reqState.State.Response() {
		return fmt.Errorf("received response body too early: %v", reqState.State)
	}

	mp.instr.Logger().Debug("message_processor: received response body: %v - EOS: %v\n", len(resp.GetBody()), resp.GetEndOfStream())

	if mp.computedBodyParsingSizeLimit.Load() <= 0 || reqState.State != MessageTypeResponseBody {
		mp.instr.Logger().Error("message_processor: the body parsing has been wrongly configured. " +
			"Please refer to the official documentation for guidance on the proper settings or contact support.")
		reqState.finalizeResponse()
		return mp.acknowledgeResponseBody(reqState, resp.GetEndOfStream())
	}

	blocked := processBody(reqState.Context, reqState.responseBuffer, resp.GetBody(), resp.GetEndOfStream(), appsec.MonitorHTTPResponseBody, "response")
	if reqState.responseBuffer.analyzed {
		// The WAF analysis is synchronous, so the retained prefix is no longer needed.
		reqState.responseBuffer.buffer = nil
		reqState.finalizeResponse()

		if (reqState.State == MessageTypeBlocked || blocked != nil) && !mp.BlockingUnavailable {
			mp.instr.Logger().Debug("external_processing: request blocked, end the stream")
			if err := mp.BlockMessageFunc(reqState.Context, reqState.blockActionLocked()); err != nil {
				return fmt.Errorf("error creating block message: %w", err)
			}
			return io.EOF
		}

		return mp.acknowledgeResponseBody(reqState, resp.GetEndOfStream())
	}

	return mp.ContinueMessageFunc(reqState.Context, ContinueActionOptions{MessageType: MessageTypeResponseBody})
}

// ackBodyMessagesUntilEndOfStream reports whether the stream must stay open to
// acknowledge body messages the analysis no longer needs.
//
// Acknowledging everything is the default because it is what every gateway got before
// this policy existed, and because it is the safe direction: it costs a round trip per
// remaining chunk, whereas closing early on a gateway that does not tolerate it causes
// timeouts. A [RequestHeaders] from a contrib module older than this core will not
// implement [bodyAcknowledgementPolicy], and must keep the behaviour it was written
// against.
func ackBodyMessagesUntilEndOfStream(ctx context.Context, req RequestHeaders) bool {
	policy, ok := req.(bodyAcknowledgementPolicy)
	if !ok {
		return true
	}

	return policy.AckBodyMessagesUntilEndOfStream(ctx)
}

// acknowledgeResponseBody acknowledges a response body message that needs no further
// analysis and reports whether the processing stream is now finished, returning io.EOF
// when it is. The caller must hold reqState.Mu and must have finalized the response
// beforehand.
//
// The acknowledgement is unconditional because the gateway is owed exactly one response
// per message it sent while the stream is open. Whether the stream may then close
// immediately or has to stay open for the rest of the body is gateway-specific, hence
// [RequestHeaders.AckBodyMessagesUntilEndOfStream].
func (mp *Processor) acknowledgeResponseBody(reqState *RequestState, endOfStream bool) error {
	lastMessage := endOfStream || !reqState.ackBodyMessagesUntilEndOfStream
	if lastMessage {
		_ = reqState.closeLocked()
	}

	if err := mp.ContinueMessageFunc(reqState.Context, ContinueActionOptions{MessageType: MessageTypeResponseBody}); err != nil {
		return fmt.Errorf("error creating continue response for response body: %w", err)
	}

	if lastMessage {
		return io.EOF
	}

	return nil
}

// OnRequestTrailers handles incoming request trailers
func (mp *Processor) OnRequestTrailers(reqState *RequestState) error {
	if reqState == nil {
		return errors.New("received a request trailer without a valid request state")
	}
	mp.instr.Logger().Debug("message_processor: received request trailers, ignoring")
	return mp.ContinueMessageFunc(reqState.Context, ContinueActionOptions{MessageType: MessageTypeRequestTrailers})
}

// OnResponseTrailers handles incoming response trailers
func (mp *Processor) OnResponseTrailers(reqState *RequestState) error {
	if reqState == nil {
		return errors.New("received a response trailer without a valid request state")
	}
	mp.instr.Logger().Debug("message_processor: received response trailers, ignoring")
	return mp.ContinueMessageFunc(reqState.Context, ContinueActionOptions{MessageType: MessageTypeResponseTrailers})
}

func processBody(ctx context.Context, bodyBuffer *bodyBuffer, body []byte, eos bool, analyzeBody func(ctx context.Context, encodable any) error, direction string) error {
	if bodyBuffer.analyzed {
		return nil
	}

	bodyBuffer.append(body)

	if eos || bodyBuffer.truncated {
		EmitBodySize(len(bodyBuffer.buffer), direction, bodyBuffer.truncated)
		bodyBuffer.analyzed = true
		return analyzeBody(ctx, json.NewEncodableFromData(bodyBuffer.buffer, bodyBuffer.truncated))
	}

	return nil
}

// isBodySupported checks if the body should be analyzed based on content type
func (mp *Processor) isBodySupported(contentType string) bool {
	if mp.computedBodyParsingSizeLimit.Load() <= 0 {
		return false
	}

	return body.IsBodySupported(contentType)
}

func (mp *Processor) Close() error {
	mp.done()
	return nil
}
