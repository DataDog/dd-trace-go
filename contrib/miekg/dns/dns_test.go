// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package dns_test

import (
	"context"
	"net"
	"testing"
	"time"

	dnstrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/miekg/dns"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"

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

func startServer(t *testing.T, traced bool) (*dns.Server, func()) {
	var h dns.Handler = &testHandler{}
	if traced {
		h = dnstrace.WrapHandler(h)
	}
	addr := getFreeAddr(t).String()
	server := &dns.Server{
		Addr:    addr,
		Net:     "udp",
		Handler: h,
	}

	// start the server
	go func() {
		err := server.ListenAndServe()
		if err != nil {
			t.Error(err)
		}
	}()
	waitTillUDPReady(addr)
	stopServer := func() {
		err := server.Shutdown()
		assert.NoError(t, err)
	}
	return server, stopServer
}

func TestExchange(t *testing.T) {
	server, stopServer := startServer(t, false)
	defer stopServer()

	mt := mocktracer.Start()
	defer mt.Stop()

	m := newMessage()

	_, err := dnstrace.Exchange(m, server.Addr)
	assert.NoError(t, err)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	assertClientSpan(t, spans[0])
}

func TestExchangeContext(t *testing.T) {
	server, stopServer := startServer(t, false)
	defer stopServer()

	mt := mocktracer.Start()
	defer mt.Stop()

	m := newMessage()

	_, err := dnstrace.ExchangeContext(context.Background(), m, server.Addr)
	assert.NoError(t, err)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	assertClientSpan(t, spans[0])
}

func TestExchangeConn(t *testing.T) {
	server, stopServer := startServer(t, false)
	defer stopServer()

	mt := mocktracer.Start()
	defer mt.Stop()

	m := newMessage()

	conn, err := net.Dial("udp", server.Addr)
	require.NoError(t, err)

	_, err = dnstrace.ExchangeConn(conn, m)
	assert.NoError(t, err)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	assertClientSpan(t, spans[0])
}

func TestClient_Exchange(t *testing.T) {
	server, stopServer := startServer(t, false)
	defer stopServer()

	mt := mocktracer.Start()
	defer mt.Stop()

	m := newMessage()

	client := newTracedClient()

	_, _, err := client.Exchange(m, server.Addr)
	assert.NoError(t, err)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	assertClientSpan(t, spans[0])
}

func TestClient_ExchangeContext(t *testing.T) {
	server, stopServer := startServer(t, false)
	defer stopServer()

	mt := mocktracer.Start()
	defer mt.Stop()

	m := newMessage()

	client := newTracedClient()

	_, _, err := client.ExchangeContext(context.Background(), m, server.Addr)
	assert.NoError(t, err)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	assertClientSpan(t, spans[0])
}

func TestWrapHandler(t *testing.T) {
	server, stopServer := startServer(t, true)

	mt := mocktracer.Start()
	defer mt.Stop()

	m := newMessage()
	_, err := dns.Exchange(m, server.Addr)
	assert.NoError(t, err)

	stopServer() // Shutdown server so span is closed after DNS request

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	span := spans[0]

	assert.Equal(t, "dns.request", span.OperationName())
	assert.Equal(t, "dns", span.Tag(ext.SpanType))
	assert.Equal(t, "dns", span.Tag(ext.ServiceName))
	assert.Equal(t, "QUERY", span.Tag(ext.ResourceName))
	assert.Equal(t, "miekg/dns", span.Tag(ext.Component))
	assert.Equal(t, ext.SpanKindServer, span.Tag(ext.SpanKind))
}

func newMessage() *dns.Msg {
	m := new(dns.Msg)
	m.SetQuestion("miek.nl.", dns.TypeMX)
	return m
}

func newTracedClient() *dnstrace.Client {
	return &dnstrace.Client{Client: &dns.Client{Net: "udp"}}
}

func assertClientSpan(t *testing.T, s mocktracer.Span) {
	assert.Equal(t, "dns.request", s.OperationName())
	assert.Equal(t, "dns", s.Tag(ext.SpanType))
	assert.Equal(t, "dns", s.Tag(ext.ServiceName))
	assert.Equal(t, "QUERY", s.Tag(ext.ResourceName))
	assert.Equal(t, "miekg/dns", s.Tag(ext.Component))
	assert.Equal(t, ext.SpanKindClient, s.Tag(ext.SpanKind))
}

func getFreeAddr(t *testing.T) net.Addr {
	li, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := li.Addr()
	li.Close()
	return addr
}

func waitTillUDPReady(addr string) {
	deadline := time.Now().Add(time.Second * 10)
	for time.Now().Before(deadline) {
		m := new(dns.Msg)
		m.SetQuestion("miek.nl.", dns.TypeMX)
		_, err := dns.Exchange(m, addr)
		if err == nil {
			break
		}
		time.Sleep(time.Millisecond * 100)
	}
}
