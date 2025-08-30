// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package message_processor

import "net/http"

// Action represents an action to be taken by the message processor.
type Action struct {
	Type     ActionType
	Response any
}

// newContinueAction creates an ActionTypeContinue without response data.
func newContinueAction() Action {
	return Action{Type: ActionTypeContinue}
}

// newContinueActionWithResponseData creates an ActionTypeContinue with header mutations and requestBody flag.
// The requestBody flag indicates whether the body should be requested from the proxy to the external processing service.
func newContinueActionWithResponseData(mutations http.Header, requestBody bool, direction Direction) Action {
	return Action{
		Type: ActionTypeContinue,
		Response: &HeadersResponseData{
			HeaderMutation: mutations,
			RequestBody:    requestBody,
			Direction:      direction,
		},
	}
}

// newBlockAction creates an ActionTypeBlock that is used to create a response and end the request
func newBlockAction(writer *fakeResponseWriter) Action {
	return Action{
		Type: ActionTypeBlock,
		Response: &BlockResponseData{
			StatusCode: writer.status,
			Headers:    writer.headers,
			Body:       writer.body,
		},
	}
}

// newFinishAction creates an ActionTypeFinish that is used to end the request without further processing.
func newFinishAction() Action {
	return Action{
		Type: ActionTypeFinish,
	}
}

// Direction indicates the direction of the message being processed.
type Direction int

const (
	DirectionRequest  Direction = iota // DirectionRequest indicates a request message.
	DirectionResponse                  // DirectionResponse indicates a response message.
)

// HeadersResponseData is the data for a headers response.
type HeadersResponseData struct {
	HeaderMutation http.Header
	RequestBody    bool
	Direction      Direction
}

// BlockResponseData is the data for a blocking response.
type BlockResponseData struct {
	StatusCode int
	Headers    http.Header
	Body       []byte
}
