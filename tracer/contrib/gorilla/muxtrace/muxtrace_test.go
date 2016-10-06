package muxtrace

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
)

func TestMuxTracer200(t *testing.T) {
	assert := assert.New(t)

	// setup
	tracer, transport := getTestTracer()
	mt := NewMuxTracer("my-service", tracer)
	r := mux.NewRouter()
	r.HandleFunc("/200", mt.TraceHandlerFunc(handler200))

	// SEnd and verify a 200 request
	req := httptest.NewRequest("GET", "/200", nil)
	writer := httptest.NewRecorder()
	r.ServeHTTP(writer, req)
	assert.Equal(writer.Code, 200)
	assert.Equal(writer.Body.String(), "200!")

	// ensure properly traced
	tracer.Flush()
	assert.Empty(transport.spans)

}

func handler200WithStatus(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(200)
	w.Write([]byte("200!"))
}

func handler200(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("200!"))
}

func handler500(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "200", http.StatusInternalServerError)
}

// getTestTracer returns a tracer which will buffer but not submit spans.
func getTestTracer() (*tracer.Tracer, *dummyTransport) {
	trans := &dummyTransport{}
	trac := tracer.NewTracerTransport(trans)
	return trac, trans
}

// dummyTransport is a transport that just buffers spans.
type dummyTransport struct {
	spans []*tracer.Span
}

func (d *dummyTransport) Send(s []*tracer.Span) error {
	d.spans = append(d.spans, s...)
	return nil
}
