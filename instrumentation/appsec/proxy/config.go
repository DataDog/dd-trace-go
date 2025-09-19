// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package proxy

import (
	"context"
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
	BodyParsingSizeLimit int
	Framework            string

	// ContinueMessageFunc is a function that generates a continue message of type O based on the provided ContinueActionOptions.
	ContinueMessageFunc func(context.Context, ContinueActionOptions) error

	// BlockMessageFunc is a function that generates a block message of type O based on the provided status code, headers, and body.
	BlockMessageFunc func(context.Context, BlockActionOptions) error
}
