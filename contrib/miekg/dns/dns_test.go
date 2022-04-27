// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package dns

import (
	"net"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/stretchr/testify/assert"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
)

type testHandler struct{}

func (th *testHandler) ServeDNS(w dns.ResponseWriter, r *dns.Msg) {
	m := new(dns.Msg)
	m.SetReply(r)
	w.WriteMsg(m)
}

func TestDNS(t *testing.T) {
	addr := getFreeAddr(t).String()
	server := dns.Server{
		Addr:    addr,
		Net:     "udp",
		Handler: WrapHandler(&testHandler{}),
	}

	// start the server
	go func() {
		err := server.ListenAndServe()
		if err != nil {
			t.Fatal(err)
		}
	}()
	waitTillUDPReady(t, addr)

	mt := mocktracer.Start()
	defer mt.Stop()

	m := new(dns.Msg)
	m.SetQuestion("miek.nl.", dns.TypeMX)

	_, err := Exchange(m, addr)
	assert.NoError(t, err)

	err = server.Shutdown() // Shutdown server so span is closed after DNS request
	assert.NoError(t, err)

	spans := mt.FinishedSpans()
	assert.Len(t, spans, 2)
	for _, s := range spans {
		assert.Equal(t, "dns.request", s.OperationName())
		assert.Equal(t, "dns", s.Tag(ext.SpanType))
		assert.Equal(t, "dns", s.Tag(ext.ServiceName))
		assert.Equal(t, "QUERY", s.Tag(ext.ResourceName))
	}
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

func waitTillUDPReady(t *testing.T, addr string) {
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
