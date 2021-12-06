// traceproftest provides testing for cross-cutting tracer/profiler features.
// It's a separate package from traceprof to avoid circular dependencies.
package traceproftest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime/pprof"
	"testing"
	"time"

	"github.com/julienschmidt/httprouter"
	httptrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/julienschmidt/httprouter"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

func startBenchApp(t testing.TB) *benchApp {
	tracer.Start(tracer.WithLogger(log.DiscardLogger{}))
	router := httptrace.New()
	app := &benchApp{
		router: router,
		req:    httptest.NewRequest("POST", "/hello", nil),
	}
	router.Handle("POST", "/hello", app.workHandler)
	app.CPUProfiler.start(t)
	return app
}

type benchApp struct {
	stopped bool
	router  *httptrace.Router
	CPUProfiler
	req *http.Request
	rw  discardWriter
}

func (b *benchApp) Request() {
	b.router.ServeHTTP(b.rw, b.req)
}

type work struct {
	CPUDuration time.Duration
	IODuration  time.Duration
}

func (b *benchApp) workHandler(rw http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	parent, _ := tracer.StartSpanFromContext(r.Context(), "child")
	defer parent.Finish()

	done := make(chan struct{})
	go func() {
		//for i := 0; i < 100; i++ {
		var m interface{}
		json.Unmarshal([]byte(`{"foo": [1, true, "bar"]}`), &m)
		json.Marshal(m)
		//}
		done <- struct{}{}
	}()
	<-done
	rw.Write([]byte("Hello World"))
	//time.Sleep(10 * time.Millisecond)
}

func (b *benchApp) Stop(t testing.TB) {
	if b.stopped {
		return
	}
	tracer.Stop()
	pprof.StopCPUProfile()
}

type discardWriter struct{}

func (d discardWriter) WriteHeader(statusCode int) {}

func (d discardWriter) Write(p []byte) (int, error) { return len(p), nil }

func (d discardWriter) Header() http.Header { return nil }
