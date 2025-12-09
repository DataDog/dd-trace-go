// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package dns_test

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	dnstrace "github.com/DataDog/dd-trace-go/contrib/miekg/dns/v2"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"

	"github.com/miekg/dns"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testHandler struct{}

func (th *testHandler) ServeDNS(w dns.ResponseWriter, r *dns.Msg) {
	m := new(dns.Msg)
	m.SetReply(r)
	w.WriteMsg(m)
}

func startServer(t *testing.T, traced bool) (*dns.Server, string) {
	var h dns.Handler = &testHandler{}
	if traced {
		h = dnstrace.WrapHandler(h)
	}
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)

	srv := &dns.Server{
		PacketConn:   pc,
		ReadTimeout:  time.Hour,
		WriteTimeout: time.Hour,
		Handler:      h,
	}

	waitLock := sync.Mutex{}
	waitLock.Lock()
	srv.NotifyStartedFunc = waitLock.Unlock

	go func() {
		require.NoError(t, srv.ActivateAndServe())
	}()
	t.Cleanup(func() {
		require.NoError(t, srv.Shutdown())
	})

	waitLock.Lock()
	return srv, pc.LocalAddr().String()
}

func TestExchange(t *testing.T) {
	_, addr := startServer(t, false)

	mt := mocktracer.Start()
	defer mt.Stop()

	m := newMessage()

	_, err := dnstrace.Exchange(m, addr)
	require.NoError(t, err)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	assertClientSpan(t, spans[0])
}

func TestExchangeContext(t *testing.T) {
	_, addr := startServer(t, false)

	mt := mocktracer.Start()
	defer mt.Stop()

	m := newMessage()

	_, err := dnstrace.ExchangeContext(context.Background(), m, addr)
	require.NoError(t, err)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	assertClientSpan(t, spans[0])
}

func TestExchangeConn(t *testing.T) {
	_, addr := startServer(t, false)

	mt := mocktracer.Start()
	defer mt.Stop()

	m := newMessage()

	conn, err := net.Dial("udp", addr)
	require.NoError(t, err)

	_, err = dnstrace.ExchangeConn(conn, m)
	require.NoError(t, err)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	assertClientSpan(t, spans[0])
}

func TestClient_Exchange(t *testing.T) {
	_, addr := startServer(t, false)

	mt := mocktracer.Start()
	defer mt.Stop()

	m := newMessage()
	client := newTracedClient()

	_, _, err := client.Exchange(m, addr)
	require.NoError(t, err)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	assertClientSpan(t, spans[0])
}

func TestClient_ExchangeContext(t *testing.T) {
	_, addr := startServer(t, false)

	mt := mocktracer.Start()
	defer mt.Stop()

	m := newMessage()
	client := newTracedClient()

	_, _, err := client.ExchangeContext(context.Background(), m, addr)
	require.NoError(t, err)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	assertClientSpan(t, spans[0])
}

func TestWrapHandler(t *testing.T) {
	_, addr := startServer(t, true)

	mt := mocktracer.Start()
	defer mt.Stop()

	m := newMessage()
	client := newClient()

	_, _, err := client.Exchange(m, addr)
	require.NoError(t, err)

	waitForSpans(mt, 1)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	span := spans[0]

	assert.Equal(t, "dns.request", span.OperationName())
	assert.Equal(t, "dns", span.Tag(ext.SpanType))
	assert.Equal(t, "dns", span.Tag(ext.ServiceName))
	assert.Equal(t, "QUERY", span.Tag(ext.ResourceName))
	assert.Equal(t, "miekg/dns", span.Tag(ext.Component))
	assert.Equal(t, "miekg/dns", span.Integration())
	assert.Equal(t, ext.SpanKindServer, span.Tag(ext.SpanKind))
}

func newMessage() *dns.Msg {
	m := new(dns.Msg)
	m.SetQuestion("miek.nl.", dns.TypeMX)
	return m
}

func newClient() *dns.Client {
	return &dns.Client{Net: "udp"}
}

func newTracedClient() *dnstrace.Client {
	return &dnstrace.Client{Client: newClient()}
}

func assertClientSpan(t *testing.T, s *mocktracer.Span) {
	assert.Equal(t, "dns.request", s.OperationName())
	assert.Equal(t, "dns", s.Tag(ext.SpanType))
	assert.Equal(t, "dns", s.Tag(ext.ServiceName))
	assert.Equal(t, "QUERY", s.Tag(ext.ResourceName))
	assert.Equal(t, "miekg/dns", s.Tag(ext.Component))
	assert.Equal(t, "miekg/dns", s.Integration())
	assert.Equal(t, ext.SpanKindClient, s.Tag(ext.SpanKind))
}

func waitForSpans(mt mocktracer.Tracer, sz int) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()

	for len(mt.FinishedSpans()) < sz {
		select {
		case <-ctx.Done():
			return
		default:
		}
		time.Sleep(time.Millisecond * 100)
	}
}
