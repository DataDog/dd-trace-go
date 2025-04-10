// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package dns

import (
	"context"
	"net"

	v2 "github.com/DataDog/dd-trace-go/contrib/miekg/dns/v2"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"

	"github.com/miekg/dns"
)

// ListenAndServe calls dns.ListenAndServe with a wrapped Handler.
func ListenAndServe(addr string, network string, handler dns.Handler) error {
	return dns.ListenAndServe(addr, network, WrapHandler(handler))
}

// ListenAndServeTLS calls dns.ListenAndServeTLS with a wrapped Handler.
func ListenAndServeTLS(addr, certFile, keyFile string, handler dns.Handler) error {
	return dns.ListenAndServeTLS(addr, certFile, keyFile, WrapHandler(handler))
}

// A Handler wraps a DNS Handler so that requests are traced.
type Handler = v2.Handler

// WrapHandler creates a new, wrapped DNS handler.
func WrapHandler(handler dns.Handler) *Handler {
	log.Debug("contrib/miekg/dns: Wrapping Handler")
	return &Handler{
		Handler: handler,
	}
}

// Exchange calls dns.Exchange and traces the request.
func Exchange(m *dns.Msg, addr string) (r *dns.Msg, err error) {
	return v2.Exchange(m, addr)
}

// ExchangeConn calls dns.ExchangeConn and traces the request.
func ExchangeConn(c net.Conn, m *dns.Msg) (r *dns.Msg, err error) {
	return v2.ExchangeConn(c, m)
}

// ExchangeContext calls dns.ExchangeContext and traces the request.
func ExchangeContext(ctx context.Context, m *dns.Msg, addr string) (r *dns.Msg, err error) {
	return v2.ExchangeContext(ctx, m, addr)
}

// A Client wraps a DNS Client so that requests are traced.
type Client = v2.Client
