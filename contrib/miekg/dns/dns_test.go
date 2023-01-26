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

	"github.com/miekg/dns"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dnstrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/miekg/dns"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
)

type testHandler struct{}

func (th *testHandler) ServeDNS(w dns.ResponseWriter, r *dns.Msg) {
	m := new(dns.Msg)
	m.SetReply(r)
	w.WriteMsg(m)
}

func startTracedServer(t *testing.T) (*dns.Server, func()) {
	addr := getFreeAddr(t).String()
	server := &dns.Server{
		Addr:    addr,
		Net:     "udp",
		Handler: dnstrace.WrapHandler(&testHandler{}),
	}

	// start the server
	go func() {
		err := server.ListenAndServe()
		if err != nil {
			t.Error(err)
		}
	}()
	waitTillUDPReady(addr)

	cleanup := func() {
		err := server.Shutdown()
		assert.NoError(t, err)
	}

	return server, cleanup
}

func TestExchange(t *testing.T) {
	server, cleanup := startTracedServer(t)
	defer cleanup()

	mt := mocktracer.Start()
	defer mt.Stop()

	m := newMessage()

	_, err := dnstrace.Exchange(m, server.Addr)
	assert.NoError(t, err)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 2)

	assertSpans(t, mt.FinishedSpans())
}

func TestExchangeContext(t *testing.T) {
	server, cleanup := startTracedServer(t)
	defer cleanup()

	mt := mocktracer.Start()
	defer mt.Stop()

	m := newMessage()

	_, err := dnstrace.ExchangeContext(context.Background(), m, server.Addr)
	assert.NoError(t, err)

	assertSpans(t, mt.FinishedSpans())
}

func TestExchangeConn(t *testing.T) {
	server, cleanup := startTracedServer(t)
	defer cleanup()

	mt := mocktracer.Start()
	defer mt.Stop()

	m := newMessage()

	conn, err := net.Dial("udp", server.Addr)
	require.NoError(t, err)

	_, err = dnstrace.ExchangeConn(conn, m)
	assert.NoError(t, err)

	assertSpans(t, mt.FinishedSpans())
}

func TestClient_Exchange(t *testing.T) {
	server, cleanup := startTracedServer(t)
	defer cleanup()

	mt := mocktracer.Start()
	defer mt.Stop()

	m := newMessage()

	client := newTracedClient()

	_, _, err := client.Exchange(m, server.Addr)
	assert.NoError(t, err)

	assertSpans(t, mt.FinishedSpans())
}

func TestClient_ExchangeContext(t *testing.T) {
	server, cleanup := startTracedServer(t)
	defer cleanup()

	mt := mocktracer.Start()
	defer mt.Stop()

	m := newMessage()

	client := newTracedClient()

	_, _, err := client.ExchangeContext(context.Background(), m, server.Addr)
	assert.NoError(t, err)

	assertSpans(t, mt.FinishedSpans())
}

func newMessage() *dns.Msg {
	m := new(dns.Msg)
	m.SetQuestion("miek.nl.", dns.TypeMX)
	return m
}

func newTracedClient() *dnstrace.Client {
	return &dnstrace.Client{Client: &dns.Client{Net: "udp"}}
}

func assertSpans(t *testing.T, spans []mocktracer.Span) {
	require.Len(t, spans, 2)

	for _, s := range spans {
		assert.Equal(t, "dns.request", s.OperationName())
		assert.Equal(t, "dns", s.Tag(ext.SpanType))
		assert.Equal(t, "dns", s.Tag(ext.ServiceName))
		assert.Equal(t, "QUERY", s.Tag(ext.ResourceName))
		assert.Equal(t, "miekg/dns", s.Tag(ext.Component))
	}

	// the server span should be the first one
	assert.Equal(t, ext.SpanKindServer, spans[0].Tag(ext.SpanKind))
	assert.Equal(t, ext.SpanKindClient, spans[1].Tag(ext.SpanKind))
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
