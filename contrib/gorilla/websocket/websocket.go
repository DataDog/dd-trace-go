// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2021 Datadog, Inc.

// Package websocket provides tracing functions for tracing the gorilla/websocket
// package (https://github.com/gorilla/websocket).
package websocket // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/gorilla/websocket"

import (
	"context"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

// Upgrader is the wrapper type of a traced upgrader.
type Upgrader struct {
	websocket.Upgrader
}

// WrapUpgrader wraps the upgrader traced with the global tracer.
func WrapUpgrader(u websocket.Upgrader) Upgrader {
	return Upgrader{Upgrader: u}
}

// Upgrade traces the actual Upgrade() method using the global tracer as span
// websocket.connection, which finishes when the returned connection is closed,
// or in case of an error.
func (u *Upgrader) Upgrade(w http.ResponseWriter, r *http.Request, responseHeader http.Header) (conn *Conn, err error) {
	span, ctx := tracer.StartSpanFromContext(r.Context(), "websocket.connection")
	defer func() {
		// Finish the span when an error occurred.
		// Otherwise, finish it in the (*Conn).Close() method.
		if err != nil {
			span.Finish(tracer.WithError(err))
		}
	}()

	tracee, err := u.Upgrader.Upgrade(w, r.WithContext(ctx), responseHeader)
	if err != nil {
		return nil, err
	}
	return wrapConn(ctx, tracee), nil
}

// Conn is the wrapper type of a traced connection.
type Conn struct {
	*websocket.Conn
	ctx context.Context
}

func wrapConn(ctx context.Context, conn *websocket.Conn) *Conn {
	return &Conn{
		Conn: conn,
		ctx:  ctx,
	}
}

// Close wraps the actual connection Close() method and finishes the
// websocket.connection span started by Upgrade().
func (c *Conn) Close() (err error) {
	if span, ok := tracer.SpanFromContext(c.ctx); ok {
		defer func() {
			span.Finish(tracer.WithError(err))
		}()
	}
	return c.Conn.Close()
}

// ReadMessage traces the actual ReadMessage() method using the global tracer
// as span websocket.read_message.
func (c *Conn) ReadMessage() (messageType int, p []byte, err error) {
	span, _ := tracer.StartSpanFromContext(c.ctx, "websocket.read_message", messageTypeTag(messageType))
	defer func() {
		span.Finish(tracer.WithError(err))
	}()
	return c.Conn.ReadMessage()
}

// ReadJSON traces the actual ReadJSON() method using the global tracer
// as span websocket.read_json.
func (c *Conn) ReadJSON(v interface{}) (err error) {
	span, _ := tracer.StartSpanFromContext(c.ctx, "websocket.read_json")
	defer func() {
		span.Finish(tracer.WithError(err))
	}()
	return c.Conn.ReadJSON(v)
}

// WriteMessage traces the actual WriteMessage() method using the global tracer
// as span websocket.write_message.
func (c *Conn) WriteMessage(messageType int, data []byte) (err error) {
	span, _ := tracer.StartSpanFromContext(c.ctx, "websocket.write_message",
		messageTypeTag(messageType), messageLengthTag(len(data)))
	defer func() {
		span.Finish(tracer.WithError(err))
	}()
	return c.Conn.WriteMessage(messageType, data)
}

// WritePreparedMessage traces the actual WritePreparedMessage() method using
// the global tracer as span websocket.write_prepared_message.
func (c *Conn) WritePreparedMessage(pm *websocket.PreparedMessage) (err error) {
	span, _ := tracer.StartSpanFromContext(c.ctx, "websocket.write_prepared_message")
	defer func() {
		span.Finish(tracer.WithError(err))
	}()
	return c.Conn.WritePreparedMessage(pm)
}

// WriteControl traces the actual WriteControl() method using the global tracer
// as span websocket.write_control.
func (c *Conn) WriteControl(messageType int, data []byte, deadline time.Time) (err error) {
	span, _ := tracer.StartSpanFromContext(c.ctx, "websocket.write_control",
		messageTypeTag(messageType), messageLengthTag(len(data)))
	defer func() {
		span.Finish(tracer.WithError(err))
	}()
	return c.Conn.WriteControl(messageType, data, deadline)
}

// WriteJSON traces the actual WriteJSON() method using the global tracer
// as span websocket.write_json.
func (c *Conn) WriteJSON(v interface{}) (err error) {
	span, _ := tracer.StartSpanFromContext(c.ctx, "websocket.write_json")
	defer func() {
		defer func() {
			span.Finish(tracer.WithError(err))
		}()
	}()
	return c.Conn.WriteJSON(v)
}

func messageTypeTag(messageType int) tracer.StartSpanOption {
	return tracer.Tag("websocket.message_type", messageType)
}

func messageLengthTag(l int) tracer.StartSpanOption {
	return tracer.Tag("websocket.message_length", l)
}
