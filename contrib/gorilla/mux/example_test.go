package mux_test

import (
	"net/http"

	muxtrace "github.com/DataDog/dd-trace-go/contrib/gorilla/mux"
)

func handler(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("Hello World!\n"))
}

func Example() {
	mux := muxtrace.NewRouter("web-service")
	mux.HandleFunc("/", handler)
	http.ListenAndServe(":8080", mux)
}
