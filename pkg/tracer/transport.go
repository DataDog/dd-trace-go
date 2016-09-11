package tracer

// Transport interface to Send spans to the given URL
type Transport interface {
	Send(url, header string, spans []*Span) error
}

// HTTPTransport provides the default implementation to send the span list using
// a HTTP/TCP connection. The transport expects to know which is the delivery URL.
// TODO: the *http implementation is missing
type HTTPTransport struct {
	URL string // the delivery URL
}

// NewHTTPTransport creates a new delivery instance that honors the Transport interface.
// This function may be useful to send data to an agent available in a remote location.
func NewHTTPTransport(url string) *HTTPTransport {
	return &HTTPTransport{
		URL: url,
	}
}

// Send is the implementation of the Transport interface and hosts the logic to send the
// spans list to a local/remote agent.
func (t *HTTPTransport) Send(url, header string, spans []*Span) error {
	if url == "" {
		return nil
	}

	// TODO: do something

	return nil
}
