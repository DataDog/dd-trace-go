// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2021 Datadog, Inc.

// Package websocket provides tracing functions for tracing the gorilla/websocket
// package (https://github.com/gorilla/websocket).
package websocket // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/gorilla/websocket"

import (
	"context"
	"time"

	"github.com/gorilla/websocket"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

// Conn is the wrapper type of a websocket connection.
type Conn struct {
	*websocket.Conn
	ctx context.Context
}

const (
	messageTypeTag   = "websocket.message_type"
	messageLengthTag = "websocket.message_length"
)

// WrapConn wraps the websocket connection to trace its methods using the global
// tracer.
func WrapConn(ctx context.Context, conn *websocket.Conn) *Conn {
	return &Conn{
		Conn: conn,
		ctx:  ctx,
	}
}

// ReadMessage is a helper method for getting a reader using NextReader and
// reading from that reader to a buffer.
func (c *Conn) ReadMessage() (messageType int, p []byte, err error) {
	span, _ := tracer.StartSpanFromContext(c.ctx, "websocket.read_message")
	defer func() {
		span.Finish(tracer.WithError(err))
	}()
	return c.Conn.ReadMessage()
}

// ReadJSON reads the next JSON-encoded message from the connection and stores
// it in the value pointed to by v.
//
// See the documentation for the encoding/json Unmarshal function for details
// about the conversion of JSON to a Go value.
func (c *Conn) ReadJSON(v interface{}) (err error) {
	span, _ := tracer.StartSpanFromContext(c.ctx, "websocket.read_json")
	defer func() {
		span.Finish(tracer.WithError(err))
	}()
	return c.Conn.ReadJSON(v)
}

// WriteMessage is a helper method for getting a writer using NextWriter,
// writing the message and closing the writer.
func (c *Conn) WriteMessage(messageType int, data []byte) (err error) {
	span, _ := tracer.StartSpanFromContext(c.ctx,
		"websocket.write_message",
		tracer.Tag(messageTypeTag, messageType),
		tracer.Tag(messageLengthTag, len(data)))
	defer func() {
		span.Finish(tracer.WithError(err))
	}()
	return c.Conn.WriteMessage(messageType, data)
}

// WritePreparedMessage writes prepared message into connection.
func (c *Conn) WritePreparedMessage(pm *websocket.PreparedMessage) (err error) {
	span, _ := tracer.StartSpanFromContext(c.ctx, "websocket.write_prepared_message")
	defer func() {
		span.Finish(tracer.WithError(err))
	}()
	return c.Conn.WritePreparedMessage(pm)
}

// WriteControl writes a control message with the given deadline. The allowed
// message types are CloseMessage, PingMessage and PongMessage.
func (c *Conn) WriteControl(messageType int, data []byte, deadline time.Time) (err error) {
	span, _ := tracer.StartSpanFromContext(c.ctx,
		"websocket.write_control",
		tracer.Tag(messageTypeTag, messageType),
		tracer.Tag(messageLengthTag, len(data)))
	defer func() {
		span.Finish(tracer.WithError(err))
	}()
	return c.Conn.WriteControl(messageType, data, deadline)
}

// WriteJSON writes the JSON encoding of v as a message.
//
// See the documentation for encoding/json Marshal for details about the
// conversion of Go values to JSON.
func (c *Conn) WriteJSON(v interface{}) (err error) {
	span, _ := tracer.StartSpanFromContext(c.ctx, "websocket.write_json")
	defer func() {
		defer func() {
			span.Finish(tracer.WithError(err))
		}()
	}()
	return c.Conn.WriteJSON(v)
}
