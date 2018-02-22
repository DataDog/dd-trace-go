package http_test

import (
	"net/http"

	httptrace "github.com/DataDog/dd-trace-go/contrib/net/http"
)

func Example() {
	mux := httptrace.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Hello World!\n"))
	})
	http.ListenAndServe(":8080", mux)
}

func Example_withServiceName() {
	mux := httptrace.NewServeMux(httptrace.WithServiceName("my-service"))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Hello World!\n"))
	})
	http.ListenAndServe(":8080", mux)
}
