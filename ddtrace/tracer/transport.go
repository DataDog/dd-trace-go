package tracer

import (
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"time"
)

var tracerVersion = "v0.7.0"

const (
	defaultHostname    = "localhost"
	defaultPort        = "8126"
	defaultAddress     = defaultHostname + ":" + defaultPort
	defaultHTTPTimeout = time.Second             // defines the current timeout before giving up with the send process
	traceCountHeader   = "X-Datadog-Trace-Count" // header containing the number of traces in the payload
)

// Transport is an interface for span submission to the agent.
type transport interface {
	sendTraces(spans [][]*span) (*http.Response, error)
}

// newTransport returns a new Transport implementation that sends traces to a
// trace agent running on the given hostname and port. If the zero values for
// hostname and port are provided, the default values will be used ("localhost"
// for hostname, and "8126" for port).
//
// In general, using this method is only necessary if you have a trace agent
// running on a non-default port or if it's located on another machine.
func newTransport(addr string) transport {
	return newHTTPTransport(addr)
}

// newDefaultTransport return a default transport for this tracing client
func newDefaultTransport() transport {
	return newHTTPTransport(defaultAddress)
}

type httpTransport struct {
	traceURL          string            // the delivery URL for traces
	legacyTraceURL    string            // the legacy delivery URL for traces
	client            *http.Client      // the HTTP client used in the POST
	headers           map[string]string // the Transport headers
	compatibilityMode bool              // the Agent targets a legacy API for compatibility reasons
	encoding          encoding          // the type of encoding used
}

// newHTTPTransport returns an httpTransport for the given endpoint
func newHTTPTransport(addr string) *httpTransport {
	// initialize the default EncoderPool with Encoder headers
	defaultHeaders := map[string]string{
		"Datadog-Meta-Lang":             "go",
		"Datadog-Meta-Lang-Version":     strings.TrimPrefix(runtime.Version(), "go"),
		"Datadog-Meta-Lang-Interpreter": runtime.Compiler + "-" + runtime.GOARCH + "-" + runtime.GOOS,
		"Datadog-Meta-Tracer-Version":   tracerVersion,
	}
	host, port, _ := net.SplitHostPort(addr)
	if host == "" {
		host = defaultHostname
	}
	if port == "" {
		port = defaultPort
	}
	addr = fmt.Sprintf("%s:%s", host, port)
	return &httpTransport{
		traceURL:       fmt.Sprintf("http://%s/v0.3/traces", addr),
		legacyTraceURL: fmt.Sprintf("http://%s/v0.2/traces", addr),
		encoding:       encodingMsgpack,
		client: &http.Client{
			// We copy the transport to avoid using the default one, as it might be
			// augmented with tracing and we don't want these calls to be recorded.
			// See https://golang.org/pkg/net/http/#DefaultTransport .
			Transport: &http.Transport{
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
			Timeout: defaultHTTPTimeout,
		},
		headers:           defaultHeaders,
		compatibilityMode: false,
	}
}

func (t *httpTransport) sendTraces(traces [][]*span) (*http.Response, error) {
	if t.traceURL == "" {
		return nil, errors.New("provided an empty URL, giving up")
	}
	r, err := encode(t.encoding, traces)
	if err != nil {
		return nil, err
	}
	// prepare the client and send the payload
	req, err := http.NewRequest("POST", t.traceURL, r)
	if err != nil {
		return nil, fmt.Errorf("cannot create http request: %v", err)
	}
	for header, value := range t.headers {
		req.Header.Set(header, value)
	}
	req.Header.Set(traceCountHeader, strconv.Itoa(len(traces)))
	req.Header.Set("Content-Type", t.encoding.contentType())
	response, err := t.client.Do(req)

	// if we have an error, return an empty Response to protect against nil pointer dereference
	if err != nil {
		return &http.Response{StatusCode: 0}, err
	}
	defer response.Body.Close()

	// if we got a 404 we should downgrade the API to a stable version (at most once)
	if (response.StatusCode == 404 || response.StatusCode == 415) && !t.compatibilityMode {
		log.Printf("calling the endpoint '%s' but received %d; downgrading the API\n", t.traceURL, response.StatusCode)
		t.apiDowngrade()
		return t.sendTraces(traces)
	}

	if sc := response.StatusCode; sc != 200 {
		return response, fmt.Errorf("sendTraces expected response code 200, received %v", sc)
	}

	return response, err
}

// apiDowngrade downgrades the used encoder and API level. This method must fallback to a safe
// encoder and API, so that it will success despite users' configurations. This action
// ensures that the compatibility mode is activated so that the downgrade will be
// executed only once.
func (t *httpTransport) apiDowngrade() {
	t.compatibilityMode = true
	t.traceURL = t.legacyTraceURL
	t.encoding = encodingJSON
}
