package tracer

import (
	"bytes"
	"errors"
	"net/http"
	"time"
)

const (
	defaultHTTPTimeout = time.Second // defines the current timeout before giving up with the send process
)

// Transport interface to Send spans to the given URL
type Transport interface {
	Send(spans []*Span) error
}

// HTTPTransport provides the default implementation to send the span list using
// a HTTP/TCP connection. The transport expects to know which is the delivery URL
// and an Encoder is used to marshal the list of spans
type HTTPTransport struct {
	URL     string       // the delivery URL
	Encoder Encoder      // the encoder used in the Marshalling process
	client  *http.Client // the HTTP client used in the POST
}

// NewHTTPTransport creates a new delivery instance that honors the Transport interface.
// This function is used to send data to an agent available in a local or remote location;
// if there is a delay during the send, the client gives up according to the defaultHTTPTimeout
// const.
func NewHTTPTransport(url string) *HTTPTransport {
	return &HTTPTransport{
		URL:     url,
		Encoder: NewJSONEncoder(),
		client: &http.Client{
			Timeout: defaultHTTPTimeout,
		},
	}
}

// Send is the implementation of the Transport interface and hosts the logic to send the
// spans list to a local/remote agent.
func (t *HTTPTransport) Send(spans []*Span) error {
	if t.URL == "" {
		return errors.New("provided an empty URL, giving up")
	}

	// encode the spans and return the error if any
	payload, err := t.Encoder.Encode(spans)
	if err != nil {
		return err
	}

	// prepare the client and send the payload
	buffReader := bytes.NewReader(payload)
	req, _ := http.NewRequest("POST", t.URL, buffReader)
	req.Header.Set("Content-Type", "application/json")
	response, err := t.client.Do(req)

	// HTTP error handling
	if err != nil {
		return err
	}

	response.Body.Close()
	return err
}
