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
	Send(spans []*Span) error
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

func (t *httpTransport) Send(spans []*Span) error {
	if t.url == "" {
		return errors.New("provided an empty URL, giving up")
	}

	// borrow an encoder
	encoder := t.pool.Borrow()
	defer t.pool.Return(encoder)

	// encode the spans and return the error if any
	err := encoder.Encode(spans)
	if err != nil {
		return err
	}

	// prepare the client and send the payload
	req, _ := http.NewRequest("POST", t.url, encoder)
	req.Header.Set("Content-Type", "application/json")
	response, err := t.client.Do(req)

	// HTTP error handling
	if err != nil {
		return err
	}

	// ignore any errors here
	_ = response.Body.Close()

	return err
}
