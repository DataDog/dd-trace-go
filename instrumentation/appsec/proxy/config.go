// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package proxy

import (
	"context"
)

const (
	// DefaultBodyParsingSizeLimit is the default number of bytes parsed for body analysis.
	DefaultBodyParsingSizeLimit = 10 * 1 << 20 // 10MB
)

// ContinueActionOptions contains options for the continue action created through [ProcessorConfig.ContinueMessageFunc].
type ContinueActionOptions struct {
	// HeaderMutations are the HTTP header mutations to be applied to the message (default is empty)
	HeaderMutations map[string][]string
	// Body indicates whether the body should be requested from the proxy to the external processing service (default is false)
	Body bool
	// MessageType indicates when the response is being created
	MessageType MessageType
}

// BlockActionOptions contains options for the block action created through [ProcessorConfig.BlockMessageFunc].
type BlockActionOptions struct {
	// StatusCode is the HTTP status code to be used in the block response, default is 403
	StatusCode int
	// Headers are the HTTP headers to be included in the block response, MUST contain at least "Content-Type"
	// if a Body is provided (default is empty)
	Headers map[string][]string
	// Body is the HTTP body to be included in the block response (default is empty)
	Body []byte
}

// ProcessorConfig contains configuration for the message processor
type ProcessorConfig struct {
	context.Context
	BlockingUnavailable  bool
	BodyParsingSizeLimit *int
	Framework            string

	// AckBodyMessagesUntilEndOfStream keeps the processing stream open until the gateway
	// signals end-of-stream, acknowledging every body message even after the analysis is
	// complete and the retained payload has been released.
	//
	// It exists because gateways disagree on what happens when the callout closes the
	// stream while body messages are still in flight. Upstream Envoy documents the early
	// close as a valid outcome, so the default (false) closes as soon as there is nothing
	// left to analyze:
	//
	//	"if the server closes the gRPC stream cleanly, then the data plane proceeds
	//	without consulting the server"
	//	- api/envoy/service/ext_proc/v3/external_processor.proto
	//
	// Google Cloud Service Extensions instead expects an acknowledgement per chunk for the
	// whole body and times the callout out otherwise, so that integration must enable this:
	//
	//	"STREAMED ... expects a single response for each chunk ... must acknowledge chunks
	//	as soon as possible"
	//	- https://docs.cloud.google.com/service-extensions/docs/callouts-overview
	//
	// Enabling it is never a protocol error, only slower: in STREAMED mode the gateway
	// withholds each chunk from the rest of the filter chain until its acknowledgement
	// comes back, so the remaining chunks of an oversized body each pay a round trip.
	AckBodyMessagesUntilEndOfStream bool

	// ContinueMessageFunc is a function that generates a continue message of type O based on the provided ContinueActionOptions.
	ContinueMessageFunc func(context.Context, ContinueActionOptions) error

	// BlockMessageFunc is a function that generates a block message of type O based on the provided status code, headers, and body.
	BlockMessageFunc func(context.Context, BlockActionOptions) error
}
