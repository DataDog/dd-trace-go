// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"time"

	pb "github.com/DataDog/datadog-agent/pkg/proto/pbgo/trace"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/internal/tracerstats"
	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/version"

	"github.com/tinylib/msgp/msgp"
)

const (
	// headerComputedTopLevel specifies that the client has marked top-level spans, when set.
	// Any non-empty value will mean 'yes'.
	headerComputedTopLevel = "Datadog-Client-Computed-Top-Level"
)

const (
	defaultHostname          = "localhost"
	defaultPort              = "8126"
	defaultOTLPPortHTTP      = "4318"
	defaultAddress           = defaultHostname + ":" + defaultPort
	defaultURL               = "http://" + defaultAddress
	defaultHTTPTimeout       = 10 * time.Second              // defines the current timeout before giving up with the send process
	traceCountHeader         = "X-Datadog-Trace-Count"       // header containing the number of traces in the payload
	obfuscationVersionHeader = "Datadog-Obfuscation-Version" // header containing the version of obfuscation used, if any

	tracesAPIPath         = "/v0.4/traces"
	tracesAPIPathV1       = "/v1.0/traces"
	statsAPIPath          = "/v0.6/stats"
	otlpTracesAPIPathHTTP = "/v1/traces"
)

// transport is an interface for communicating data to the agent.
type transport interface {
	// send sends the payload p to the agent using the transport set up.
	// It returns a non-nil response body when no error occurred.
	send(p payload) (body io.ReadCloser, err error)
	// sendStats sends the given stats payload to the agent.
	// tracerObfuscationVersion is the version of obfuscation applied (0 if none was applied)
	sendStats(s *pb.ClientStatsPayload, tracerObfuscationVersion int) error
	// endpoint returns the URL to which the transport will send traces.
	endpoint() string
}

type httpTransport struct {
	traceURL string            // the delivery URL for traces
	statsURL string            // the delivery URL for stats
	client   *http.Client      // the HTTP client used in the POST
	headers  map[string]string // the Transport headers
}

// newTransport returns a new Transport implementation that sends traces to a
// trace agent at the given url, using a given *http.Client.
//
// In general, using this method is only necessary if you have a trace agent
// running on a non-default port, if it's located on another machine, or when
// otherwise needing to customize the transport layer, for instance when using
// a unix domain socket.
func newHTTPTransport(url string, client *http.Client) *httpTransport {
	// initialize the default EncoderPool with Encoder headers
	defaultHeaders := map[string]string{
		"Datadog-Meta-Lang":             "go",
		"Datadog-Meta-Lang-Version":     strings.TrimPrefix(runtime.Version(), "go"),
		"Datadog-Meta-Lang-Interpreter": runtime.Compiler + "-" + runtime.GOARCH + "-" + runtime.GOOS,
		"Datadog-Meta-Tracer-Version":   version.Tag,
		"Content-Type":                  "application/msgpack",
	}
	if cid := internal.ContainerID(); cid != "" {
		defaultHeaders["Datadog-Container-ID"] = cid
	}
	if eid := internal.EntityID(); eid != "" {
		defaultHeaders["Datadog-Entity-ID"] = eid
	}
	if extEnv := internal.ExternalEnvironment(); extEnv != "" {
		defaultHeaders["Datadog-External-Env"] = extEnv
	}
	return &httpTransport{
		traceURL: fmt.Sprintf("%s%s", url, tracesAPIPath),
		statsURL: fmt.Sprintf("%s%s", url, statsAPIPath),
		client:   client,
		headers:  defaultHeaders,
	}
}

func (t *httpTransport) sendStats(p *pb.ClientStatsPayload, tracerObfuscationVersion int) error {
	var buf bytes.Buffer
	if err := msgp.Encode(&buf, p); err != nil {
		return err
	}
	req, err := http.NewRequest("POST", t.statsURL, &buf)
	if err != nil {
		return err
	}
	for header, value := range t.headers {
		req.Header.Set(header, value)
	}
	if tracerObfuscationVersion > 0 {
		req.Header.Set(obfuscationVersionHeader, strconv.Itoa(tracerObfuscationVersion))
	}
	resp, err := t.client.Do(req)
	if err != nil {
		reportAPIErrorsMetric(resp, err, statsAPIPath)
		return err
	}
	defer resp.Body.Close()
	if code := resp.StatusCode; code >= 400 {
		reportAPIErrorsMetric(resp, err, statsAPIPath)
		// error, check the body for context information and
		// return a nice error.
		msg := make([]byte, 1000)
		n, _ := resp.Body.Read(msg)
		resp.Body.Close()
		txt := http.StatusText(code)
		if n > 0 {
			return fmt.Errorf("%s (Status: %s)", msg[:n], txt)
		}
		return fmt.Errorf("%s", txt)
	}
	return nil
}

func (t *httpTransport) send(p payload) (body io.ReadCloser, err error) {
	stats := p.stats()
	isOTLP := p.protocol() == traceProtocolOTLP

	// When the body size is unknown upfront (payloadOTLP encodes lazily, size() == -1),
	// buffer the encoded bytes before creating the request. This lets us set an accurate
	// Content-Length, avoiding chunked transfer encoding. Some OTLP collectors and proxies
	// require a known Content-Length and will hang indefinitely on chunked requests, which
	// manifests as "context deadline exceeded while awaiting headers".
	var reqBody io.Reader = p
	contentLength := int64(stats.size)
	if stats.size < 0 {
		var buf bytes.Buffer
		if _, err = io.Copy(&buf, p); err != nil {
			return nil, fmt.Errorf("failed to buffer payload body: %s", err)
		}
		contentLength = int64(buf.Len())
		reqBody = &buf
		log.Debug("Buffered OTLP payload before send: %d bytes, %d traces, url: %s", contentLength, stats.itemCount, t.traceURL)
	}

	req, err := http.NewRequest("POST", t.traceURL, reqBody)
	if err != nil {
		return nil, fmt.Errorf("cannot create http request: %s", err)
	}
	req.ContentLength = contentLength
	for header, value := range t.headers {
		req.Header.Set(header, value)
	}

	// DD-specific sampling / stats headers are not understood by OTLP collectors.
	if !isOTLP {
		req.Header.Set(traceCountHeader, strconv.Itoa(stats.itemCount))
		req.Header.Set(headerComputedTopLevel, "t")
		if tr := getGlobalTracer(); tr != nil {
			tc := tr.TracerConf()
			if tc.TracingAsTransport || tc.CanComputeStats {
				// tracingAsTransport uses this header to disable the trace agent's stats computation
				// while making canComputeStats() always false to also disable client stats computation.
				req.Header.Set("Datadog-Client-Computed-Stats", "t")
			}
			droppedTraces := int(tracerstats.Count(tracerstats.AgentDroppedP0Traces))
			partialTraces := int(tracerstats.Count(tracerstats.PartialTraces))
			droppedSpans := int(tracerstats.Count(tracerstats.AgentDroppedP0Spans))
			if tt, ok := tr.(*tracer); ok {
				if stats := tt.statsd; stats != nil {
					stats.Count("datadog.tracer.dropped_p0_traces", int64(droppedTraces),
						[]string{fmt.Sprintf("partial:%s", strconv.FormatBool(partialTraces > 0))}, 1)
					stats.Count("datadog.tracer.dropped_p0_spans", int64(droppedSpans), nil, 1)
				}
			}
			req.Header.Set("Datadog-Client-Dropped-P0-Traces", strconv.Itoa(droppedTraces))
			req.Header.Set("Datadog-Client-Dropped-P0-Spans", strconv.Itoa(droppedSpans))
		}
	}

	log.Debug("Sending %d traces to %s (Content-Length: %d)", stats.itemCount, t.traceURL, contentLength)
	response, err := t.client.Do(req)
	if err != nil {
		reportAPIErrorsMetric(response, err, tracesAPIPath)
		return nil, err
	}
	if code := response.StatusCode; code >= 400 {
		reportAPIErrorsMetric(response, err, tracesAPIPath)
		// error, check the body for context information and
		// return a nice error.
		msg := make([]byte, 1000)
		n, _ := response.Body.Read(msg)
		response.Body.Close()
		txt := http.StatusText(code)
		if n > 0 {
			return nil, fmt.Errorf("%s (Status: %s)", msg[:n], txt)
		}
		return nil, fmt.Errorf("%s", txt)
	}
	return response.Body, nil
}

func reportAPIErrorsMetric(response *http.Response, err error, endpoint string) {
	if t, ok := getGlobalTracer().(*tracer); ok {
		var reason string
		if err != nil {
			reason = "network_failure"
		}
		if response != nil {
			reason = fmt.Sprintf("server_response_%d", response.StatusCode)
		}
		tags := []string{"reason:" + reason, "endpoint:" + endpoint}
		t.statsd.Incr("datadog.tracer.api.errors", tags, 1)
	} else {
		return
	}
}

func (t *httpTransport) endpoint() string {
	return t.traceURL
}
