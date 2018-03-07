package mux_test

import (
	"net/http"

	muxtrace "gopkg.in/DataDog/dd-trace-go.v0/contrib/gorilla/mux"
)

func handler(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("Hello World!\n"))
}

func Example() {
	mux := muxtrace.NewRouter()
	mux.HandleFunc("/", handler)
	http.ListenAndServe(":8080", mux)
}

func Example_withServiceName() {
	mux := muxtrace.NewRouter(muxtrace.WithServiceName("mux.route"))
	mux.HandleFunc("/", handler)
	http.ListenAndServe(":8080", mux)
}
