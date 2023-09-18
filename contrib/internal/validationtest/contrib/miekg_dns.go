// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package validationtest

import (
	"context"
	"net"
	"testing"
	"time"

	dnstrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/miekg/dns"

	"github.com/miekg/dns"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type DNS struct {
	msg      *dns.Msg
	mux      *dns.ServeMux
	addr     string
	numSpans int
}

func NewDNS() *DNS {
	return &DNS{}
}

func (i *DNS) Name() string {
	return "miekg/dns"
}

func (i *DNS) Init(t *testing.T) {
	// TODO: enable when the integration implements naming schema
	t.Skip("not implemented yet")

	t.Helper()
	i.addr = getFreeAddr(t).String()
	server := &dns.Server{
		Addr:    i.addr,
		Net:     "udp",
		Handler: dnstrace.WrapHandler(&handler{t: t, ig: i}),
	}
	// start the traced server
	go func() {
		require.NoError(t, server.ListenAndServe())
	}()
	// wait for the server to be ready
	waitServerReady(t, server.Addr)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		assert.NoError(t, server.ShutdownContext(ctx))
	})
}

func (i *DNS) GenSpans(t *testing.T) {
	t.Helper()
	msg := newMessage()
	_, err := dnstrace.Exchange(msg, i.addr)
	require.NoError(t, err)
	i.numSpans++
	t.Cleanup(func() {
		i.numSpans = 0
	})
}

func (i *DNS) NumSpans() int {
	return i.numSpans
}

func (i *DNS) WithServiceName(_ string) {
	return
}

func newMessage() *dns.Msg {
	m := new(dns.Msg)
	m.SetQuestion("miek.nl.", dns.TypeMX)
	return m
}

type handler struct {
	t  *testing.T
	ig *DNS
}

func (h *handler) ServeDNS(w dns.ResponseWriter, r *dns.Msg) {
	m := new(dns.Msg)
	m.SetReply(r)
	assert.NoError(h.t, w.WriteMsg(m))
	h.ig.numSpans++
}

func getFreeAddr(t *testing.T) net.Addr {
	li, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := li.Addr()
	require.NoError(t, li.Close())
	return addr
}

func waitServerReady(t *testing.T, addr string) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	timeoutChan := time.After(5 * time.Second)
	for {
		m := new(dns.Msg)
		m.SetQuestion("miek.nl.", dns.TypeMX)
		_, err := dns.Exchange(m, addr)
		if err == nil {
			break
		}

		select {
		case <-ticker.C:
			continue

		case <-timeoutChan:
			t.Fatal("timeout waiting for DNS server to be ready")
		}
	}
}
