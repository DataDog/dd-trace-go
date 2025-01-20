// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package internal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"runtime"
	"sync"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/hostname"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/newtelemetry/internal/transport"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/osinfo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/version"
)

// We copy the transport to avoid using the default one, as it might be
// augmented with tracing and we don't want these calls to be recorded.
// See https://golang.org/pkg/net/http/#DefaultTransport .
//
//orchestrion:ignore
var defaultHTTPClient = &http.Client{
	Transport: &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	},
	Timeout: 5 * time.Second,
}

func newBody(config TracerConfig, debugMode bool) *transport.Body {
	osHostname, err := os.Hostname()
	if err != nil {
		osHostname = hostname.Get()
	}

	if osHostname == "" {
		osHostname = "unknown" // hostname field is not allowed to be empty
	}

	return &transport.Body{
		APIVersion: "v2",
		RuntimeID:  globalconfig.RuntimeID(),
		Debug:      debugMode,
		Application: transport.Application{
			ServiceName:     config.Service,
			Env:             config.Env,
			ServiceVersion:  config.Version,
			TracerVersion:   version.Tag,
			LanguageName:    "go",
			LanguageVersion: runtime.Version(),
		},
		Host: transport.Host{
			Hostname:      osHostname,
			OS:            osinfo.OSName(),
			OSVersion:     osinfo.OSVersion(),
			Architecture:  osinfo.Architecture(),
			KernelName:    osinfo.KernelName(),
			KernelRelease: osinfo.KernelRelease(),
			KernelVersion: osinfo.KernelVersion(),
		},
	}
}

// Writer is an interface that allows to send telemetry data to any endpoint that implements the instrumentation telemetry v2 API.
// The telemetry data is sent as a JSON payload as described in the API documentation.
type Writer interface {
	// Flush does a synchronous call to the telemetry endpoint with the given payload. Thread-safe.
	// It returns the number of bytes sent and an error if any.
	// Keep in mind that errors can be returned even if the payload was sent successfully.
	// Please check if the number of bytes sent is greater than 0 to know if the payload was sent.
	Flush(transport.Payload) (int, error)
}

type writer struct {
	mu         sync.Mutex
	body       *transport.Body
	httpClient *http.Client
	endpoints  []*http.Request
}

type WriterConfig struct {
	// TracerConfig is the configuration the tracer sent when the telemetry client was created (required)
	TracerConfig
	// Endpoints is a list of requests that will be used alongside the body of the telemetry data to create the requests to the telemetry endpoint (required to not be empty)
	// The writer will try each endpoint in order until it gets a 2XX HTTP response from the server
	Endpoints []*http.Request
	// HTTPClient is the http client that will be used to send the telemetry data (defaults to a copy of [http.DefaultClient])
	HTTPClient *http.Client
	// Debug is a flag that indicates whether the telemetry client is in debug mode (defaults to false)
	Debug bool
}

func NewWriter(config WriterConfig) (Writer, error) {
	if config.HTTPClient == nil {
		config.HTTPClient = defaultHTTPClient
	}

	// Don't allow the client to have a timeout higher than 5 seconds
	// This is to avoid blocking the client for too long in case of network issues
	if config.HTTPClient.Timeout > 5*time.Second {
		copyClient := *config.HTTPClient
		config.HTTPClient = &copyClient
		config.HTTPClient.Timeout = 5 * time.Second
	}

	if len(config.Endpoints) == 0 {
		return nil, fmt.Errorf("telemetry/writer: no endpoints provided")
	}

	body := newBody(config.TracerConfig, config.Debug)
	endpoints := make([]*http.Request, len(config.Endpoints))
	for i, endpoint := range config.Endpoints {
		endpoints[i] = preBakeRequest(body, endpoint)
	}

	return &writer{
		body:       body,
		httpClient: config.HTTPClient,
		endpoints:  endpoints,
	}, nil
}

// preBakeRequest adds all the *static* headers that we already know at the time of the creation of the writer.
// This is useful to avoid querying too many things at the time of the request.
// Headers necessary are described here:
// https://github.com/DataDog/instrumentation-telemetry-api-docs/blob/cf17b41a30fbf31d54e2cfbfc983875d58b02fe1/GeneratedDocumentation/ApiDocs/v2/overview.md#required-http-headers
func preBakeRequest(body *transport.Body, endpoint *http.Request) *http.Request {
	clonedEndpoint := endpoint.Clone(context.Background())
	if clonedEndpoint.Header == nil {
		clonedEndpoint.Header = make(http.Header, 11)
	}

	for key, val := range map[string]string{
		"Content-Type":               "application/json",
		"DD-Telemetry-API-Version":   body.APIVersion,
		"DD-Client-Library-Language": body.Application.LanguageName,
		"DD-Client-Library-Version":  body.Application.TracerVersion,
		"DD-Agent-Env":               body.Application.Env,
		"DD-Agent-Hostname":          body.Host.Hostname,
		"DD-Agent-Install-Id":        globalconfig.InstrumentationInstallID(),
		"DD-Agent-Install-Type":      globalconfig.InstrumentationInstallType(),
		"DD-Agent-Install-Time":      globalconfig.InstrumentationInstallTime(),
		"Datadog-Container-ID":       internal.ContainerID(),
		"Datadog-Entity-ID":          internal.EntityID(),
		// TODO: Add support for Cloud provider/resource-type/resource-id headers in another PR and package
		// Described here: https://github.com/DataDog/instrumentation-telemetry-api-docs/blob/cf17b41a30fbf31d54e2cfbfc983875d58b02fe1/GeneratedDocumentation/ApiDocs/v2/overview.md#setting-the-serverless-telemetry-headers
	} {
		if val == "" {
			continue
		}
		clonedEndpoint.Header.Add(key, val)
	}

	if body.Debug {
		clonedEndpoint.Header.Add("DD-Telemetry-Debug-Enabled", "true")
	}

	return clonedEndpoint
}

// newRequest creates a new http.Request with the given payload and the necessary headers.
func (w *writer) newRequest(endpoint *http.Request, payload transport.Payload) *http.Request {
	pipeReader, pipeWriter := io.Pipe()
	request := endpoint.Clone(context.Background())

	w.body.SeqID++
	w.body.TracerTime = time.Now().Unix()
	w.body.RequestType = payload.RequestType()
	w.body.Payload = payload

	request.Body = pipeReader
	request.Header.Set("DD-Telemetry-Request-Type", string(payload.RequestType()))

	go func() {
		// No need to wait on this because the http client will close the pipeReader which will close the pipeWriter and finish the goroutine
		pipeWriter.CloseWithError(json.NewEncoder(pipeWriter).Encode(w.body))
	}()

	return request
}

// SumReaderCloser is a ReadCloser that wraps another ReadCloser and counts the number of bytes read.
type SumReaderCloser struct {
	io.ReadCloser
	n *int
}

func (s *SumReaderCloser) Read(p []byte) (n int, err error) {
	n, err = s.ReadCloser.Read(p)
	*s.n += n
	return
}

func (w *writer) Flush(payload transport.Payload) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	var (
		errs    []error
		sumRead int
	)
	for _, endpoint := range w.endpoints {
		request := w.newRequest(endpoint, payload)
		request.Body = &SumReaderCloser{ReadCloser: request.Body, n: &sumRead}
		response, err := w.httpClient.Do(request)
		if err != nil {
			errs = append(errs, fmt.Errorf("telemetry/writer: %w", err))
			sumRead = 0
			continue
		}

		// Currently we have a maximum of 3 endpoints so we can afford to close bodies at the end of the function
		//goland:noinspection GoDeferInLoop
		defer response.Body.Close()

		if response.StatusCode >= 300 || response.StatusCode < 200 {
			respBodyBytes, _ := io.ReadAll(response.Body) // maybe we can find an error reason in the response body
			errs = append(errs, fmt.Errorf("telemetry/writer: unexpected status code: %q (received body: %q)", response.Status, string(respBodyBytes)))
			sumRead = 0
			continue
		}

		// We succeeded, no need to try the other endpoints
		break
	}

	return sumRead, errors.Join(errs...)
}

// RecordWriter is a Writer that stores the payloads in memory. Used for testing purposes
type RecordWriter struct {
	mu       sync.Mutex
	payloads []transport.Payload
}

func (w *RecordWriter) Flush(payload transport.Payload) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.payloads = append(w.payloads, payload)
	return nil
}
