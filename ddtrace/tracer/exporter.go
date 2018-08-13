// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadog.com/).
// Copyright 2018 Datadog, Inc.

package tracer

import (
	"bytes"
	"fmt"
	"net"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"
)

// payloadLimit specifies the maximum payload size that the Datadog
// agent will accept. Request bodies larger than this will be rejected.
const payloadLimit = int(1e7) // 10MB

var (
	// inChannelSize specifies the size of the buffered channel which
	// takes spans and adds them to the payload.
	inChannelSize = int(5e5) // 500K (approx 61MB memory if full)

	// flushThreshold specifies the payload's size threshold in bytes. If it
	// is exceeded, a flush will be triggered.
	flushThreshold = payloadLimit / 2

	// flushInterval specifies the interval at which the payload will
	// automatically be flushed.
	flushInterval = 2 * time.Second
)

var tracerVersion = "v2.0alpha"

type exporter interface {
	exportSpan(*span)
}

type defaultExporter struct {
	addr    string
	payload *packedSpans
	client  *http.Client
	errors  *errorAmortizer

	// uploadFn specifies the function used for uploading.
	// Defaults to (*transport).upload; replaced in tests.
	uploadFn func(pkg *bytes.Buffer, count uint64) error

	wg   sync.WaitGroup // counts active uploads
	in   chan *span
	exit chan struct{}
}

func newDefaultExporter(agentAddr string) exporter {
	client := &http.Client{
		Transport: &http.Transport{
			// copy of http.DefaultTransport
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   30 * time.Second,
				KeepAlive: 30 * time.Second,
				DualStack: true,
			}).DialContext,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		},
		Timeout: time.Second,
	}
	errors := newErrorAmortizer(defaultErrorFreq, nil)
	e := &defaultExporter{
		errors:  errors,
		payload: new(packedSpans),
		addr:    resolveAgentAddr(agentAddr),
		client:  client,
		in:      make(chan *span, inChannelSize),
		exit:    make(chan struct{}),
	}
	e.uploadFn = e.upload
	go e.loop()
	return e
}

func (e *defaultExporter) loop() {
	defer close(e.exit)
	tick := time.NewTicker(flushInterval)
	defer tick.Stop()

	for {
		select {
		case span := <-e.in:
			if err := e.payload.add(span); err != nil {
				e.errors.log(errorTypeEncoding, err)
			}
			if e.payload.size() > flushThreshold {
				e.flush()
			}

		case <-tick.C:
			e.flush()

		case <-e.exit:
			e.flush()
			e.wg.Wait() // wait for uploads to finish
			e.errors.flush()
			return
		}
	}
}

func (e *defaultExporter) exportSpan(s *span) {
	select {
	case e.in <- s:
	default:
		e.errors.log(errorTypeOverflow, nil)
	}
}

func (e *defaultExporter) flush() {
	n := e.payload.count
	if n == 0 {
		return
	}
	buf := e.payload.buffer()
	e.wg.Add(1)
	go func() {
		if err := e.uploadFn(buf, n); err != nil {
			e.errors.log(errorTypeTransport, err)
		}
		e.wg.Done()
	}()
	e.payload.reset()
}

var headers = map[string]string{
	"Datadog-Meta-Lang":             "go",
	"Datadog-Meta-Lang-Version":     strings.TrimPrefix(runtime.Version(), "go"),
	"Datadog-Meta-Lang-Interpreter": runtime.Compiler + "-" + runtime.GOARCH + "-" + runtime.GOOS,
	"Datadog-Meta-Tracer-Version":   tracerVersion,
	"Content-Type":                  "application/msgpack",
}

func (e *defaultExporter) upload(buf *bytes.Buffer, count uint64) error {
	req, err := http.NewRequest("POST", e.addr, buf)
	if err != nil {
		return fmt.Errorf("cannot create http request: %v", err)
	}
	for header, value := range headers {
		req.Header.Set(header, value)
	}
	response, err := e.client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if code := response.StatusCode; code >= 400 {
		// error, check the body for context information and
		// return a nice error.
		msg := make([]byte, 1000)
		n, _ := response.Body.Read(msg)
		txt := http.StatusText(code)
		if n > 0 {
			return fmt.Errorf("%s (Status: %s)", msg[:n], txt)
		}
		return fmt.Errorf("%s", txt)
	}
	return nil
}

// Flush cleanly stops the exporter, flushing any remaining spans to the transport and
// reporting any errors. Make sure to always call Stop at the end of your program in
// order to not lose any tracing data.
func (e *defaultExporter) Flush() {
	select {
	case <-e.exit:
		return
	default:
		e.exit <- struct{}{}
		<-e.exit
	}
}

const (
	defaultHostname = "localhost"
	defaultPort     = "8126"
	defaultAddress  = defaultHostname + ":" + defaultPort
)

// resolveAgentAddr resolves the given agent address and fills in any missing host
// and port using the defaults.
func resolveAgentAddr(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		// no port in addr
		host = addr
	}
	if host == "" {
		host = defaultHostname
	}
	if port == "" {
		port = defaultPort
	}
	return fmt.Sprintf("http://%s:%s/v1/spans", host, port)
}

type noopExporter struct{}

func (noopExporter) exportSpan(_ *span) {}
