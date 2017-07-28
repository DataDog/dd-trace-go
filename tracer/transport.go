package tracer

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"
)

const (
	defaultHostname    = "localhost"
	defaultPort        = "8126"
	defaultEncoder     = MSGPACK_ENCODER         // defines the default encoder used when the Transport is initialized
	legacyEncoder      = JSON_ENCODER            // defines the legacy encoder used with earlier agent versions
	defaultHTTPTimeout = time.Second             // defines the current timeout before giving up with the send process
	encoderPoolSize    = 5                       // how many encoders are available
	traceCountHeader   = "X-Datadog-Trace-Count" // header containing the number of traces in the payload
)

// Transport is an interface for span submission to the agent.
type Transport interface {
	SendTraces(spans [][]*Span) error
	SendServices(services map[string]Service) error
	SetHeader(key, value string)
}

// NewTransport returns a new Transport implementation that sends traces to a
// trace agent running on the given hostname and port. If the zero values for
// hostname and port are provided, the default values will be used ("localhost"
// for hostname, and "8126" for port).
//
// In general, using this method is only necessary if you have a trace agent
// running on a non-default port or if it's located on another machine.
func NewTransport(hostname, port string) Transport {
	if hostname == "" {
		hostname = defaultHostname
	}
	if port == "" {
		port = defaultPort
	}
	return newHTTPTransport(hostname, port)
}

// newDefaultTransport return a default transport for this tracing client
func newDefaultTransport() Transport {
	return newHTTPTransport(defaultHostname, defaultPort)
}

type httpTransport struct {
	traceURL          string            // the delivery URL for traces
	legacyTraceURL    string            // the legacy delivery URL for traces
	serviceURL        string            // the delivery URL for services
	legacyServiceURL  string            // the legacy delivery URL for services
	pool              *encoderPool      // encoding allocates lot of buffers (which might then be resized) so we use a pool so they can be re-used
	client            *http.Client      // the HTTP client used in the POST
	headers           map[string]string // the Transport headers
	compatibilityMode bool              // the Agent targets a legacy API for compatibility reasons
}

// newHTTPTransport returns an httpTransport for the given endpoint
func newHTTPTransport(hostname, port string) *httpTransport {
	// initialize the default EncoderPool with Encoder headers
	pool, contentType := newEncoderPool(defaultEncoder, encoderPoolSize)
	defaultHeaders := make(map[string]string)
	defaultHeaders["Content-Type"] = contentType

	return &httpTransport{
		traceURL:         fmt.Sprintf("http://%s:%s/v0.3/traces", hostname, port),
		legacyTraceURL:   fmt.Sprintf("http://%s:%s/v0.2/traces", hostname, port),
		serviceURL:       fmt.Sprintf("http://%s:%s/v0.3/services", hostname, port),
		legacyServiceURL: fmt.Sprintf("http://%s:%s/v0.2/services", hostname, port),
		pool:             pool,
		client: &http.Client{
			Timeout: defaultHTTPTimeout,
		},
		headers:           defaultHeaders,
		compatibilityMode: false,
	}
}

func (t *httpTransport) SendTraces(traces [][]*Span) error {
	if t.traceURL == "" {
		return errors.New("provided an empty URL, giving up")
	}

	// borrow an encoder
	encoder := t.pool.Borrow()
	defer t.pool.Return(encoder)

	// encode the spans and return the error if any
	err := encoder.EncodeTraces(traces)
	if err != nil {
		// TODO: errors.wrap?
		return err
	}

	// prepare the client and send the payload
	req, _ := http.NewRequest("POST", t.traceURL, encoder)
	for header, value := range t.headers {
		req.Header.Set(header, value)
	}
	req.Header.Set(traceCountHeader, strconv.Itoa(len(traces)))
	response, err := t.client.Do(req)

	if err != nil {
		return err
	}
	defer response.Body.Close()

	// if we got a 404 we should downgrade the API to a stable version (at most once)
	if (response.StatusCode == 404 || response.StatusCode == 415) && !t.compatibilityMode {
		log.Printf("calling the endpoint '%s' but received %d; downgrading the API\n", t.traceURL, response.StatusCode)
		t.apiDowngrade()
		return t.SendTraces(traces)
	}

	if sc := response.StatusCode; sc != 200 {
		return fmt.Errorf("SendTraces expected response code 200, received %v", sc)
	}

	return err
}

func (t *httpTransport) SendServices(services map[string]Service) error {
	if t.serviceURL == "" {
		return errors.New("provided an empty URL, giving up")
	}

	// Encode the service table
	encoder := t.pool.Borrow()
	defer t.pool.Return(encoder)

	if err := encoder.EncodeServices(services); err != nil {
		return err
	}

	// Send it
	req, err := http.NewRequest("POST", t.serviceURL, encoder)
	if err != nil {
		return fmt.Errorf("cannot create http request: %v", err)
	}
	for header, value := range t.headers {
		req.Header.Set(header, value)
	}

	response, err := t.client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	// Downgrade if necessary
	if (response.StatusCode == 404 || response.StatusCode == 415) && !t.compatibilityMode {
		log.Printf("calling the endpoint '%s' but received %d; downgrading the API\n", t.traceURL, response.StatusCode)
		t.apiDowngrade()
		return t.SendServices(services)
	}

	if sc := response.StatusCode; sc != 200 {
		return fmt.Errorf("SendServices expected response code 200, received %v", sc)
	}

	return err
}

// SetHeader sets the internal header for the httpTransport
func (t *httpTransport) SetHeader(key, value string) {
	t.headers[key] = value
}

// changeEncoder switches the internal encoders pool so that a different API with different
// format can be targeted, preventing failures because of outdated agents
func (t *httpTransport) changeEncoder(encoderType int) {
	pool, contentType := newEncoderPool(encoderType, encoderPoolSize)
	t.pool = pool
	t.headers["Content-Type"] = contentType
}

// apiDowngrade downgrades the used encoder and API level. This method must fallback to a safe
// encoder and API, so that it will success despite users' configurations. This action
// ensures that the compatibility mode is activated so that the downgrade will be
// executed only once.
func (t *httpTransport) apiDowngrade() {
	t.compatibilityMode = true
	t.traceURL = t.legacyTraceURL
	t.serviceURL = t.legacyServiceURL
	t.changeEncoder(legacyEncoder)
}
