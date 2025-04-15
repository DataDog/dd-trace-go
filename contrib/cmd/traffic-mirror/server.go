// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"time"

	"github.com/puzpuzpuz/xsync/v3"

	"github.com/DataDog/dd-trace-go/v2/appsec"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/httptrace"
)

type server struct {
	cfg Config

	// idToStream is a map of request IDs to channels that will receive the requests (that are responses actually)
	idToStream xsync.MapOf[string, chan *http.Request]
}

func newServer(cfg Config) *server {
	serv := &server{
		cfg: cfg,
	}

	return serv
}

// forceClose closes the connection to the client by hijacking the connection so we don't send any response.
func (s *server) forceClose(w http.ResponseWriter) {
	wr, ok := w.(http.Hijacker)
	if !ok {
		instr.Logger().Error("ResponseWriter does not support Hijack")
		os.Exit(1)
	}

	conn, _, err := wr.Hijack()
	if err != nil {
		instr.Logger().Error("Failed to hijack connection: %v", err)
		os.Exit(1)
	}

	conn.Close()
}

// analyzeRequestBody check if the body can be parsed and if so, parse it and send it to the WAF
// and return if blocking was performed on the http.ResponseWriter
func (s *server) analyzeRequestBody(r *http.Request) bool {
	if r.Body == nil {
		instr.Logger().Debug("Request body is nil")
		return false
	}

	if r.ContentLength == 0 {
		instr.Logger().Debug("Request body is empty")
		return false
	}

	var (
		body any
		err  error
	)

	// Check if the body is a valid JSON
	switch r.Header.Get("Content-Type") {
	case "application/json":
		body = make(map[string]any)
		err = json.NewDecoder(r.Body).Decode(&body)
	}

	if err != nil {
		instr.Logger().Debug("Failed to parse request body: %v", err)
		return false
	}

	if body == nil {
		return false
	}

	return appsec.MonitorParsedHTTPBody(r.Context(), body) != nil
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	logger := instr.Logger()
	if s.cfg.Features.NoResponse {
		s.forceClose(w)
		return
	}

	if s.cfg.Features.ResponseAsRequest {
		// if ResponseAsRequest is enabled, we need to check if it's a response
		requestId := s.cfg.RequestIDFunc(r)
		if requestId == "" {
			logger.Error("Request ID is empty")
			return
		}

		responseChan, ok := s.idToStream.Load(requestId)
		if ok {
			responseChan <- r
			logger.Debug("Response sent to channel")
			return
		}
	}

	_, _, afterHandle, blocked := httptrace.BeforeHandle(s.cfg.ServeConfig, w, r)
	defer afterHandle()

	if s.cfg.Features.Body {
		blocked = blocked || s.analyzeRequestBody(r)
	}

	if blocked {
		logger.Debug("Request blocked by WAF")
		return
	}

	if s.cfg.Features.ResponseAsRequest {
		// If ResponseAsRequest is enabled, we need to wait for the response to be sent
		requestId := s.cfg.RequestIDFunc(r)
		if requestId == "" {
			logger.Error("Request ID is empty")
			return
		}

		responseChan := make(chan *http.Request, 1)
		s.idToStream.Store(requestId, responseChan)
		defer s.idToStream.Delete(requestId)

		ctx, cancel := context.WithDeadline(r.Context(), time.Now().Add(s.cfg.Timeout))
		defer cancel()

		select {
		case <-responseChan:
			logger.Debug("Response received to channel")
			// TODO Support sending response body to the WAF in internal/appsec
		case <-ctx.Done():
			logger.Error("Failed to send response to channel: %v", ctx.Err())
		}
	}

	if s.cfg.Features.Blocking && !blocked {
		// if blocking is enabled but the request was not blocked we need to stop net/http from sending a 200 OK response
		s.forceClose(w)
		return
	}
}
