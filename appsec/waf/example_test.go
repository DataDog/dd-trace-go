package waf_test

import (
	httptrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/net/http"
	"net/http"
	"net/http/httptest"
)

func ExampleWAF() {
	mux := httptrace.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Hello World!\n"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	req, err := http.NewRequest("POST", srv.URL+"/?attack=<script>alert()</script>", nil)
	if err != nil {
		panic(err)
	}
	res, err := srv.Client().Do(req)
	_, _ = res, err
	// Output:
}
