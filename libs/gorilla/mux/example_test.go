package mux_test

import (
	"net/http"

	httptrace "github.com/DataDog/dd-trace-go/libs/net/http"
	"github.com/gorilla/mux"
)

func handler(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("Hello World!\n"))
}

func Example() {
	mux := mux.NewRouter()
	mux.HandleFunc("/", handler)

	// you just need to use our net/http integration to trace gorilla/mux
	traceHandler := httptrace.NewHandler(mux, "web-service", nil)
	http.ListenAndServe(":8080", traceHandler)
}
