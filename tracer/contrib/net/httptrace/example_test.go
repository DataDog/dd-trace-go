package httptrace_test

import (
	"fmt"
	"net/http"

	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/DataDog/dd-trace-go/tracer/contrib/net/httptrace"
)

func handler(w http.ResponseWriter, r *http.Request) {
	span := tracer.SpanFromContextDefault(r.Context())
	fmt.Printf("tracing service:%s resource:%s", span.Service, span.Resource)
	w.Write([]byte("hello world"))
}

func Example() {
	mux := http.NewServeMux()
	mux.Handle("/users", hander)
	mux.Handle("/anything", hander)
	httpTracer := httptrace.NewHttpTracer("fake-service", tracer.DefaultTracer)

	http.ListenAndServe(":8080", httpTracer.Handler(mux))
}
