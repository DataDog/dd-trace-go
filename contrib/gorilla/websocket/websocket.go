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

type Upgrader struct {
	websocket.Upgrader
}

func Wrap(u websocket.Upgrader) Upgrader {
	return Upgrader{Upgrader: u}
}

type Conn struct {
	*websocket.Conn
	ctx context.Context
}

func (u *Upgrader) Upgrade(w http.ResponseWriter, r *http.Request, responseHeader http.Header) (*Conn, error) {
	conn, err := u.Upgrader.Upgrade(w, r, responseHeader)
	if err != nil {
		return nil, err
	}
	return &Conn{
		Conn: conn,
		ctx:  r.Context(),
	}, nil
}

func (c *Conn) ReadMessage() (messageType int, p []byte, err error) {
	span, _ := tracer.StartSpanFromContext(c.ctx, "websocket.read_message", messageTypeTag(messageType))
	defer func() {
		var opt tracer.FinishOption
		if err != nil {
			opt = tracer.WithError(err)
		}
		span.Finish(opt)
	}()
	return c.Conn.ReadMessage()
}

func (c *Conn) ReadJSON(v interface{}) (err error) {
	span, _ := tracer.StartSpanFromContext(c.ctx, "websocket.read_json")
	defer func() {
		var opt tracer.FinishOption
		if err != nil {
			opt = tracer.WithError(err)
		}
		span.Finish(opt)
	}()
	return c.Conn.ReadJSON(v)
}

func (c *Conn) WriteMessage(messageType int, data []byte) (err error) {
	span, _ := tracer.StartSpanFromContext(c.ctx, "websocket.write_message",
		messageTypeTag(messageType), messageLengthTag(len(data)))
	defer func() {
		var opt tracer.FinishOption
		if err != nil {
			opt = tracer.WithError(err)
		}
		span.Finish(opt)
	}()
	return c.Conn.WriteMessage(messageType, data)
}

func (c *Conn) WritePreparedMessage(pm *websocket.PreparedMessage) (err error) {
	span, _ := tracer.StartSpanFromContext(c.ctx, "websocket.write_prepared_message")
	defer func() {
		var opt tracer.FinishOption
		if err != nil {
			opt = tracer.WithError(err)
		}
		span.Finish(opt)
	}()
	return c.Conn.WritePreparedMessage(pm)
}

func (c *Conn) WriteControl(messageType int, data []byte, deadline time.Time) (err error) {
	span, _ := tracer.StartSpanFromContext(c.ctx, "websocket.write_control",
		messageTypeTag(messageType), messageLengthTag(len(data)))
	defer func() {
		var opt tracer.FinishOption
		if err != nil {
			opt = tracer.WithError(err)
		}
		span.Finish(opt)
	}()
	return c.Conn.WriteControl(messageType, data, deadline)
}

func (c *Conn) WriteJSON(v interface{}) (err error) {
	span, _ := tracer.StartSpanFromContext(c.ctx, "websocket.write_json")
	defer func() {
		var opt tracer.FinishOption
		if err != nil {
			opt = tracer.WithError(err)
		}
		span.Finish(opt)
	}()
	return c.Conn.WriteJSON(v)
}

func messageTypeTag(messageType int) tracer.StartSpanOption {
	return tracer.Tag("websocket.message_type", messageType)
}

func messageLengthTag(l int) tracer.StartSpanOption {
	return tracer.Tag("websocket.message_length", l)
}
