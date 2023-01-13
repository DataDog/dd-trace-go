package httpmem_test

import (
	"net/http"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/httpmem"
)

func TestServerAndClient(t *testing.T) {
	s, c := httpmem.ServerAndClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer s.Close()
	r, err := http.NewRequest("GET", "http://foo/bar", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Do(r); err != nil {
		t.Fatal(err)
	}
}

func TestServerClosed(t *testing.T) {
	s, c := httpmem.ServerAndClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	s.Close()
	r, err := http.NewRequest("GET", "http://foo/bar", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Do(r); err == nil {
		t.Fatal("request should have failed, but it succeeded")
	}
}
