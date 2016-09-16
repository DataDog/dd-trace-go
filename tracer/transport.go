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

// transport interface to Send spans to the tracer agent
type transport interface {
	Send(spans []*Span) error
}

// httpTransport provides the default implementation to send the span list using
// a HTTP/TCP connection. The transport expects to know which is the delivery URL
// and an Encoder is used to marshal the list of spans
type httpTransport struct {
	url    string       // the delivery URL
	pool   *encoderPool // encoding allocates lot of buffers (which might then be resized) so we use a pool so they can be re-used
	client *http.Client // the HTTP client used in the POST
}

// newHTTPTransport creates a new delivery instance that honors the Transport interface.
// This function is used to send data to an agent available in a local or remote location;
// if there is a delay during the send, the client gives up according to the defaultHTTPTimeout
// const.
func newHTTPTransport(url string) *httpTransport {
	return &httpTransport{
		url:  url,
		pool: newEncoderPool(encoderPoolSize),
		client: &http.Client{
			Timeout: defaultHTTPTimeout,
		},
	}
}

// Send is the implementation of the Transport interface and hosts the logic to send the
// spans list to a local/remote agent.
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

	response.Body.Close()
	return err
}
