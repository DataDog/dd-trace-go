// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2021 Datadog, Inc.

package nats

import (
	"context"
	"net/url"
	"os"
	"strconv"
	"testing"
	"time"

	natsd "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
)

var (
	conn        *nats.Conn
	wrappedConn *Conn
)

func TestMain(m *testing.M) {
	natsURL, _ := url.Parse("nats://127.0.0.1:4223")
	natsPort, _ := strconv.Atoi(natsURL.Port())

	s, err := natsd.NewServer(&natsd.Options{
		Host:    natsURL.Hostname(),
		Port:    natsPort,
		LogFile: "/dev/stdout",
	})
	if err != nil {
		panic(err)
	}

	go s.Start()

	if !s.ReadyForConnections(10 * time.Second) {
		panic("nats server didn't start")
	}

	conn, err = nats.Connect(natsURL.String())
	if err != nil {
		panic(err)
	}

	wrappedConn = WrapConn(conn, WithServiceName("natstest"))
	defer wrappedConn.Close()

	os.Exit(m.Run())
}

func validateSpanGenerics(t *testing.T, span mocktracer.Span, resourceName string) {
	assert.Equal(t, "natstest", span.Tag(ext.ServiceName))
	assert.Equal(t, "nats.query", span.OperationName())
	assert.Equal(t, resourceName, span.Tag(ext.ResourceName))
}

func TestPublishMsg(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	err := wrappedConn.PublishMsg(context.Background(), &nats.Msg{
		Subject: "foo",
		Data:    []byte("bar"),
	})
	assert.NoError(t, err)

	spans := mt.FinishedSpans()
	assert.Len(t, spans, 1)
	validateSpanGenerics(t, spans[0], "publish")
}

func TestRequestMsg(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	s, err := wrappedConn.SubscribeSync("request_respond")
	if err != nil {
		panic(err)
	}
	defer s.Drain()

	go func(t *testing.T) {
		msg, err := s.NextMsg(context.Background(), 5*time.Second)
		require.NoError(t, err)
		require.NotNil(t, msg)
		//require.NotNil(t, msg.Msg)

		err = msg.RespondMsg(&nats.Msg{
			Data: []byte("baz"),
		})
		assert.NoError(t, err)
	}(t)

	resp, err := wrappedConn.RequestMsg(
		context.Background(),
		&nats.Msg{
			Subject: "request_respond",
			Data:    []byte("bar"),
		},
		time.Second,
	)

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.NotNil(t, resp.Msg)

	spans := mt.FinishedSpans()
	assert.Len(t, spans, 3)
	validateSpanGenerics(t, spans[0], "subscription.nextmsg")
	validateSpanGenerics(t, spans[1], "msg.respond")
	validateSpanGenerics(t, spans[2], "request")
	assert.Equal(t, spans[1].ParentID(), spans[2].TraceID())
}
