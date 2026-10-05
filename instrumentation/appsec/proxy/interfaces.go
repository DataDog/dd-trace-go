// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package proxy

import (
	"context"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

type InputMessage interface {
	GetEndOfStream() bool
	MessageType() MessageType
}

// RequestHeaders is an interface for accessing request headers used by the message processor.
type RequestHeaders interface {
	InputMessage

	// ExtractRequest extracts the pseudo headers and other relevant information from the request.
	ExtractRequest(context.Context) (PseudoRequest, error)

	// SpanOptions returns additional options to use when starting the span for this request.
	SpanOptions(context.Context) []tracer.StartSpanOption

	// BodyParsingSizeLimit returns the default value for body processing based on the request.
	BodyParsingSizeLimit(ctx context.Context) int
}

// bodyAcknowledgementPolicy is an optional interface a [RequestHeaders] implementation
// may satisfy to opt out of acknowledging response body messages it no longer needs.
//
// It is deliberately optional rather than part of [RequestHeaders]. The contribs that
// implement that interface are separate Go modules, so making this a required method
// would stop an older contrib from compiling against a newer core — a build failure on
// version skew, not just a change for out-of-tree implementations. An implementation
// that does not satisfy this interface keeps the previous behaviour.
//
// Gateways disagree on what happens when the callout closes the stream while body
// messages are still in flight. Upstream Envoy documents the early close as a valid
// outcome, so returning false lets the stream end as soon as the analysis is done:
//
//	"if the server closes the gRPC stream cleanly, then the data plane proceeds
//	without consulting the server"
//	- api/envoy/service/ext_proc/v3/external_processor.proto
//
// Google Cloud Service Extensions instead expects an acknowledgement per chunk for the
// whole body and times the callout out otherwise, so it must return true:
//
//	"STREAMED ... expects a single response for each chunk ... must acknowledge chunks
//	as soon as possible"
//	- https://docs.cloud.google.com/service-extensions/docs/callouts-overview
//
// Returning true is never a protocol error, only slower: in STREAMED mode the gateway
// withholds each chunk from the rest of the filter chain until its acknowledgement
// comes back, so the remaining chunks of an oversized body each pay a round trip.
type bodyAcknowledgementPolicy interface {
	AckBodyMessagesUntilEndOfStream(ctx context.Context) bool
}

// HTTPBody is an interface for accessing the body of an HTTP message used by the message processor.
// Whenever request or response body. It can be only a fraction of the full body. and multiple of these messages can be sent.
type HTTPBody interface {
	InputMessage
	GetBody() []byte
}

// ResponseHeaders is an interface for accessing response headers used by the message processor.
type ResponseHeaders interface {
	InputMessage

	// ExtractResponse extracts the response headers and other relevant information from the response.
	ExtractResponse() (PseudoResponse, error)
}

// MessageType defines the type of message being processed.
type MessageType int

const (
	_ MessageType = iota
	MessageTypeRequestHeaders
	MessageTypeRequestBody
	MessageTypeRequestTrailers
	MessageTypeResponseHeaders
	MessageTypeResponseBody
	MessageTypeResponseTrailers
	MessageTypeBlocked
	MessageTypeFinished
)

func (mt MessageType) Ongoing() bool {
	return mt != MessageTypeFinished && mt != MessageTypeBlocked && mt != 0
}

func (mt MessageType) Request() bool {
	return mt == MessageTypeRequestHeaders || mt == MessageTypeRequestBody || mt == MessageTypeRequestTrailers
}

func (mt MessageType) Response() bool {
	return mt == MessageTypeResponseHeaders || mt == MessageTypeResponseBody || mt == MessageTypeResponseTrailers
}

func (mt MessageType) String() string {
	switch mt {
	case MessageTypeRequestHeaders:
		return "<RequestHeaders>"
	case MessageTypeRequestBody:
		return "<RequestBody>"
	case MessageTypeRequestTrailers:
		return "<RequestTrailers>"
	case MessageTypeResponseHeaders:
		return "<ResponseHeaders>"
	case MessageTypeResponseBody:
		return "<ResponseBody>"
	case MessageTypeResponseTrailers:
		return "<ResponseTrailers>"
	case MessageTypeBlocked:
		return "<Blocked>"
	case MessageTypeFinished:
		return "<Finished>"
	default:
		return "<Unknown>"
	}
}
