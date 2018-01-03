package chi_test

import (
	"fmt"
	"net/http"

	chitrace "github.com/DataDog/dd-trace-go/contrib/go-chi/chi"
	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/go-chi/chi"
)

// To start tracing requests, add the trace middleware to your Chi routes.
func Example() {
	// Create your router and use the middleware.
	r := chi.NewRouter()
	middleware := chitrace.Middleware("my-web-app", "GET /hello", tracer.DefaultTracer)
	r.With(middleware).Get("/hello", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "(っ^‿^)っ")
	})

	http.ListenAndServe(":1234", r)
}
