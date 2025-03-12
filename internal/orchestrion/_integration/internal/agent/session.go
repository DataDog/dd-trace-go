// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package agent

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

type Session struct {
	agent *MockAgent
	token uuid.UUID
}

func (s *Session) Port() int {
	return s.agent.port
}

func (s *Session) Close(t testing.TB) ([]byte, error) {
	if !s.agent.currentSession.CompareAndSwap(s, nil) {
		return nil, errors.New("cannot close session that is not the currently active one")
	}

	tracer.Flush()
	tracer.Stop()

	t.Logf("Closing test session with ID %s\n", s.token.String())
	resp, err := internalClient.Get(fmt.Sprintf("http://127.0.0.1:%d/test/session/traces?test_session_token=%s", s.agent.port, s.token.String()))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return data, nil
}

var (
	defaultTransport, _ = http.DefaultTransport.(*http.Transport)
	// A copy of the default transport, except it will be marked internal by orchestrion, so it is not traced.
	internalTransport = &http.Transport{
		Proxy:                 defaultTransport.Proxy,
		DialContext:           defaultTransport.DialContext,
		ForceAttemptHTTP2:     defaultTransport.ForceAttemptHTTP2,
		MaxIdleConns:          defaultTransport.MaxIdleConns,
		IdleConnTimeout:       defaultTransport.IdleConnTimeout,
		TLSHandshakeTimeout:   defaultTransport.TLSHandshakeTimeout,
		ExpectContinueTimeout: defaultTransport.ExpectContinueTimeout,
	}

	internalClient = http.Client{Transport: internalTransport}
)

func (s *Session) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Set("X-Datadog-Test-Session-Token", s.token.String())
	return internalTransport.RoundTrip(req)
}
