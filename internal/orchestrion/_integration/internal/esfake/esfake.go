// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package esfake answers Elasticsearch client requests with canned responses.
// It reads and writes HTTP on plain connections instead of running a
// net/http server, so the only spans a request produces are the client's.
package esfake

import (
	"bufio"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
)

// info is the answer to GET /, which go-elasticsearch v7 sends before the
// first request of each client to check that it talks to Elasticsearch.
const info = `{"version":{"number":"7.17.1","build_flavor":"default"},"tagline":"You Know, for Search"}`

// Server is a fake Elasticsearch node.
type Server struct {
	// URL is the node address to configure clients with.
	URL string

	ln net.Listener
}

// Start starts a node on a local port. With a non-nil tlsConfig, the node
// serves TLS and URL uses https.
func Start(tlsConfig *tls.Config) (*Server, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	scheme := "http"
	if tlsConfig != nil {
		ln = tls.NewListener(ln, tlsConfig)
		scheme = "https"
	}
	s := &Server{URL: scheme + "://" + ln.Addr().String(), ln: ln}
	go s.serve()
	return s, nil
}

// Close stops accepting connections.
func (s *Server) Close() error {
	return s.ln.Close()
}

func (s *Server) serve() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go handle(conn)
	}
}

func handle(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	for {
		req, err := http.ReadRequest(r)
		if err != nil {
			return
		}
		_, _ = io.Copy(io.Discard, req.Body)
		_ = req.Body.Close()

		body := `{"acknowledged":true}`
		if req.Method == http.MethodGet && req.URL.Path == "/" {
			body = info
		}
		res := &http.Response{
			StatusCode:    http.StatusOK,
			ProtoMajor:    1,
			ProtoMinor:    1,
			Header:        http.Header{},
			Body:          io.NopCloser(strings.NewReader(body)),
			ContentLength: int64(len(body)),
		}
		res.Header.Set("Content-Type", "application/json")
		res.Header.Set("Content-Length", strconv.Itoa(len(body)))
		res.Header.Set("X-Elastic-Product", "Elasticsearch")
		if err := res.Write(conn); err != nil {
			return
		}
	}
}
