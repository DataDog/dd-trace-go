package tracer

import (
	"errors"
	"net/http"
	"time"
)

const (
	defaultHTTPTimeout = time.Second // defines the current timeout before giving up with the send process
	encoderPoolSize    = 5           // how many encoders are available
)

// Transport is an interface for span submission to the agent.
type Transport interface {
	Send(spans [][]*Span) (*http.Response, error)
}

type httpTransport struct {
	url    string       // the delivery URL
	pool   *encoderPool // encoding allocates lot of buffers (which might then be resized) so we use a pool so they can be re-used
	client *http.Client // the HTTP client used in the POST
}

func newHTTPTransport(url string) *httpTransport {
	return &httpTransport{
		url:  url,
		pool: newEncoderPool(encoderPoolSize),
		client: &http.Client{
			Timeout: defaultHTTPTimeout,
		},
	}
}

func (t *httpTransport) Send(traces [][]*Span) (*http.Response, error) {
	if t.url == "" {
		return nil, errors.New("provided an empty URL, giving up")
	}

	// borrow an encoder
	encoder := t.pool.Borrow()
	defer t.pool.Return(encoder)

	// encode the spans and return the error if any
	err := encoder.Encode(traces)
	if err != nil {
		return nil, err
	}

	// prepare the client and send the payload
	req, _ := http.NewRequest("POST", t.url, encoder)
	req.Header.Set("Content-Type", "application/json")
	response, err := t.client.Do(req)

	// if we have an error, return an empty Response to protect against nil pointer dereference
	if err != nil {
		return &http.Response{StatusCode: 0}, err
	}

	response.Body.Close()
	return response, err
}
