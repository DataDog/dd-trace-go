package tracing

import "net/http"

// responseWriter is a small wrapper around an http response writer that will
// intercept and store the status of a request.
type responseWriter struct {
	http.ResponseWriter
	status int
}

func newResponseWriter(w http.ResponseWriter) *responseWriter {
	return &responseWriter{w, 0}
}

// Status returns the status code that was monitored.
func (w *responseWriter) Status() int {
	return w.status
}

// Write writes the data to the connection as part of an HTTP reply.
// We explicitly call WriteHeader with the 200 status code
// in order to get it reported into the span.
func (w *responseWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

// WriteHeader sends an HTTP response header with status code.
// It also sets the status code to the span.
func (w *responseWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.ResponseWriter.WriteHeader(status)
	w.status = status
}

// Unwrap returns the underlying wrapped http.ResponseWriter.
func (w *responseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}
