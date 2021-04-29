// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2021 Datadog, Inc.

package websocket_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	websockettrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/gorilla/websocket"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

func TestWrapConn(t *testing.T) {
	for _, tc := range []struct {
		name           string
		client, server func(*websockettrace.Conn)
		assert         func(mt mocktracer.Tracer)
	}{
		{
			name: "WriteMessage/ReadMessage",
			client: func(conn *websockettrace.Conn) {
				mt, msg, err := conn.ReadMessage()
				require.NoError(t, err)
				require.Equal(t, websocket.TextMessage, mt)
				require.Equal(t, []byte("message"), msg)
			},
			server: func(conn *websockettrace.Conn) {
				err := conn.WriteMessage(websocket.TextMessage, []byte("message"))
				require.NoError(t, err)
			},
			assert: func(mt mocktracer.Tracer) {
				require.Empty(t, mt.OpenSpans())

				spans := mt.FinishedSpans()
				require.Len(t, spans, 2)

				span := spans[0]
				require.Equal(t, "websocket.write_message", span.OperationName())
				require.Equal(t, len("message"), span.Tag("websocket.message_length"))
				require.Equal(t, websocket.TextMessage, span.Tag("websocket.message_type"))

				span = spans[1]
				require.Equal(t, "websocket.read_message", span.OperationName())
			},
		},
		{
			name: "WritePrepareMessage",
			client: func(conn *websockettrace.Conn) {
				mt, msg, err := conn.ReadMessage()
				require.NoError(t, err)
				require.Equal(t, websocket.TextMessage, mt)
				require.Equal(t, []byte("message"), msg)
			},
			server: func(conn *websockettrace.Conn) {
				msg, err := websocket.NewPreparedMessage(websocket.TextMessage, []byte("message"))
				err = conn.WritePreparedMessage(msg)
				require.NoError(t, err)
			},
			assert: func(mt mocktracer.Tracer) {
				require.Empty(t, mt.OpenSpans())

				spans := mt.FinishedSpans()
				require.Len(t, spans, 2)

				span := spans[0]
				require.Equal(t, "websocket.write_prepared_message", span.OperationName())

				span = spans[1]
				require.Equal(t, "websocket.read_message", span.OperationName())
			},
		},
		{
			name: "WriteJSON/ReadJSON",
			client: func(conn *websockettrace.Conn) {
				var s string
				err := conn.ReadJSON(&s)
				require.NoError(t, err)
				require.Equal(t, "message", s)
			},
			server: func(conn *websockettrace.Conn) {
				err := conn.WriteJSON("message")
				require.NoError(t, err)
			},
			assert: func(mt mocktracer.Tracer) {
				require.Empty(t, mt.OpenSpans())

				spans := mt.FinishedSpans()
				require.Len(t, spans, 2)

				span := spans[0]
				require.Equal(t, "websocket.write_json", span.OperationName())

				span = spans[1]
				require.Equal(t, "websocket.read_json", span.OperationName())
			},
		},
		{
			name:   "WriteControl",
			client: func(*websockettrace.Conn) {},
			server: func(conn *websockettrace.Conn) {
				err := conn.WriteControl(websocket.PingMessage, []byte("message"), time.Now().Add(time.Second))
				require.NoError(t, err)
			},
			assert: func(mt mocktracer.Tracer) {
				require.Empty(t, mt.OpenSpans())

				spans := mt.FinishedSpans()
				require.Len(t, spans, 1)

				span := spans[0]
				require.Equal(t, "websocket.write_control", span.OperationName())
				require.Equal(t, len("message"), span.Tag("websocket.message_length"))
				require.Equal(t, websocket.PingMessage, span.Tag("websocket.message_type"))
			},
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			runClientServer(t, tc.client, tc.server)
			tc.assert(mt)
		})
	}
}

func runClientServer(t *testing.T, client, server func(conn *websockettrace.Conn)) {
	// Create an HTTP server handling websocket connections served by function
	// `server`.
	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		require.NoError(t, err)
		defer conn.Close()
		server(websockettrace.WrapConn(r.Context(), conn))
	}))
	defer srv.Close()

	// Create a client websocket connection served by function `client`
	dialer := websocket.Dialer{}
	u, err := url.Parse(srv.URL)
	require.NoError(t, err)
	u.Scheme = "ws"
	wconn, _, err := dialer.Dial(u.String(), nil)
	require.NoError(t, err)
	defer wconn.Close()
	client(websockettrace.WrapConn(context.Background(), wconn))
}
