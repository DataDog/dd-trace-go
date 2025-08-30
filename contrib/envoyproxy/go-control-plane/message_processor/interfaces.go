// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package message_processor

import (
	"context"
	"net/http"
)

// RequestHeaders is an interface for accessing request headers used by the message processor.
type RequestHeaders interface {
	NewRequest(context.Context) (*http.Request, error)
	EndOfStream() bool
	Component(context.Context) string
	Framework() string
}

// RequestBody is an interface for accessing the request body used by the message processor.
type RequestBody interface {
	Body() []byte
	EndOfStream() bool
}

// ResponseHeaders is an interface for accessing response headers used by the message processor.
type ResponseHeaders interface {
	InitResponseWriter(http.ResponseWriter) error
	EndOfStream() bool
}

// ResponseBody is an interface for accessing the response body used by the message processor.
type ResponseBody interface {
	Body() []byte
	EndOfStream() bool
}

// ActionType defines the type of Action to be taken.
type ActionType int

const (
	ActionTypeContinue ActionType = iota
	ActionTypeBlock
	ActionTypeFinish
)

// MessageProcessorConfig contains configuration for the message processor
type MessageProcessorConfig struct {
	BlockingUnavailable  bool
	BodyParsingSizeLimit int
}
