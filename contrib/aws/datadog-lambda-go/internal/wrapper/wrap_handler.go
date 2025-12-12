// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package wrapper

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/DataDog/dd-trace-go/contrib/aws/datadog-lambda-go/v2/internal/extension"
	"github.com/DataDog/dd-trace-go/contrib/aws/datadog-lambda-go/v2/internal/logger"
	"github.com/aws/aws-lambda-go/lambda"

	"reflect"
)

var (
	// CurrentContext is the last create lambda context object.
	CurrentContext context.Context
)

type (
	// HandlerListener is a point where listener logic can be injected into a handler
	HandlerListener interface {
		HandlerStarted(ctx context.Context, msg json.RawMessage) context.Context
		HandlerFinished(ctx context.Context, err error)
	}

	DatadogHandler struct {
		coldStart bool
		handler   lambda.Handler
		listeners []HandlerListener
	}
)

// WrapHandlerWithListeners wraps a lambda handler, and calls listeners before and after every invocation.
func WrapHandlerWithListeners(handler interface{}, listeners ...HandlerListener) interface{} {
	err := validateHandler(handler)
	if err != nil {
		// This wasn't a valid handler function, pass back to AWS SDK to let it handle the error.
		logger.Error(fmt.Errorf("handler function was in format ddlambda doesn't recognize: %v", err))
		return handler
	}
	coldStart := true

	// Return custom handler, to be called once per invocation
	return func(ctx context.Context, msg json.RawMessage) (interface{}, error) {
		//nolint
		ctx = context.WithValue(ctx, "cold_start", coldStart)
		for _, listener := range listeners {
			ctx = listener.HandlerStarted(ctx, msg)
		}
		CurrentContext = ctx
		result, err := callHandler(ctx, msg, handler)
		for _, listener := range listeners {
			ctx = context.WithValue(ctx, extension.DdLambdaResponse, result)
			listener.HandlerFinished(ctx, err)
		}
		coldStart = false
		CurrentContext = nil
		return result, err
	}
}

func (h *DatadogHandler) Invoke(ctx context.Context, payload []byte) ([]byte, error) {
	//nolint
	ctx = context.WithValue(ctx, "cold_start", h.coldStart)
	msg := json.RawMessage{}
	err := msg.UnmarshalJSON(payload)
	if err != nil {
		logger.Error(fmt.Errorf("couldn't load handler payload: %v", err))
	}

	for _, listener := range h.listeners {
		ctx = listener.HandlerStarted(ctx, msg)
	}

	CurrentContext = ctx
	result, err := h.handler.Invoke(ctx, payload)
	for _, listener := range h.listeners {
		listener.HandlerFinished(ctx, err)
	}
	h.coldStart = false
	CurrentContext = nil
	return result, err
}

func WrapHandlerInterfaceWithListeners(handler lambda.Handler, listeners ...HandlerListener) lambda.Handler {
	return &DatadogHandler{
		coldStart: true,
		handler:   handler,
		listeners: listeners,
	}
}

func validateHandler(handler interface{}) error {
	// Detect the handler follows the right format, based on the GO AWS SDK.
	// https://docs.aws.amazon.com/lambda/latest/dg/go-programming-model-handler-types.html
	handlerType := reflect.TypeOf(handler)
	if handlerType.Kind() != reflect.Func {
		return errors.New("handler is not a function")
	}

	if handlerType.NumIn() == 2 {
		contextType := reflect.TypeOf((*context.Context)(nil)).Elem()
		firstArgType := handlerType.In(0)
		if !firstArgType.Implements(contextType) {
			return errors.New("handler should take context as first argument")
		}
	}
	if handlerType.NumIn() > 2 {
		return errors.New("handler takes too many arguments")
	}

	errorType := reflect.TypeOf((*error)(nil)).Elem()

	if handlerType.NumOut() > 2 {
		return errors.New("handler returns more than two values")
	}
	if handlerType.NumOut() > 0 {
		rt := handlerType.Out(handlerType.NumOut() - 1) // Last returned value
		if !rt.Implements(errorType) {
			return errors.New("handler doesn't return error as it's last value")
		}
	}
	return nil
}

func callHandler(ctx context.Context, msg json.RawMessage, handler interface{}) (interface{}, error) {
	ev, err := unmarshalEventForHandler(msg, handler)
	if err != nil {
		return nil, err
	}
	handlerType := reflect.TypeOf(handler)

	args := []reflect.Value{}

	if handlerType.NumIn() == 1 {
		// When there is only one argument, argument is either the event payload, or the context.
		contextType := reflect.TypeOf((*context.Context)(nil)).Elem()
		firstArgType := handlerType.In(0)
		if firstArgType.Implements(contextType) {
			args = []reflect.Value{reflect.ValueOf(ctx)}
		} else {
			args = []reflect.Value{ev.Elem()}

		}
	} else if handlerType.NumIn() == 2 {
		// Or when there are two arguments, context is always first, followed by event payload.
		args = []reflect.Value{reflect.ValueOf(ctx), ev.Elem()}
	}

	handlerValue := reflect.ValueOf(handler)
	output := handlerValue.Call(args)

	var response interface{}
	var errResponse error

	if len(output) > 0 {
		// If there are any output values, the last should always be an error
		val := output[len(output)-1].Interface()
		if errVal, ok := val.(error); ok {
			errResponse = errVal
		}
	}

	if len(output) > 1 {
		// If there is more than one output value, the first should be the response payload.
		response = output[0].Interface()
	}

	return response, errResponse
}

func unmarshalEventForHandler(ev json.RawMessage, handler interface{}) (reflect.Value, error) {
	handlerType := reflect.TypeOf(handler)
	if handlerType.NumIn() == 0 {
		return reflect.ValueOf(nil), nil
	}

	messageType := handlerType.In(handlerType.NumIn() - 1)
	contextType := reflect.TypeOf((*context.Context)(nil)).Elem()
	firstArgType := handlerType.In(0)

	if handlerType.NumIn() == 1 && firstArgType.Implements(contextType) {
		return reflect.ValueOf(nil), nil
	}

	newMessage := reflect.New(messageType)
	err := json.Unmarshal(ev, newMessage.Interface())
	if err != nil {
		return reflect.ValueOf(nil), err
	}
	return newMessage, err
}
