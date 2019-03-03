package chi_test

import (
	"net/http"

	chitrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/chi"
)

func handler(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("Hello World!\n"))
}

func Example() {
	router := chitrace.NewRouter()
	router.HandleFunc("/", handler)
	http.ListenAndServe(":8080", router)
}

func Example_withServiceName() {
	router := chitrace.NewRouter(chitrace.WithServiceName("chi.route"))
	router.HandleFunc("/", handler)
	http.ListenAndServe(":8080", router)
}
