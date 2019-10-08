package vault_test

import (
	"net/http"

	"github.com/hashicorp/vault/api"
	"gopkg.in/DataDog/dd-trace-go.v1/contrib/hashicorp/vault"
	httptrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/net/http"
)

func ExampleNewHTTPClient() (*api.Client, error) {
	vaultClient, err := api.NewClient(&api.Config{
		HttpClient: vault.NewHTTPClient(),
		Address:    "http://vault.mydomain.com:8200",
	})
	return vaultClient, err
}

// Options can be passed in to configure the tracer
func ExampleNewHTTPClient_withOptions() (*api.Client, error) {
	vaultClient, err := api.NewClient(
		&api.Config{
			HttpClient: vault.NewHTTPClient(
				httptrace.RTWithAnalytics(true),
				httptrace.RTWithAnalyticsRate(1.0)),
			Address: "http://vault.mydomain.com:8200",
		})
	return vaultClient, err
}

func ExampleWrapHTTPTransport() (*api.Client, error) {
	var myHttpClient *http.Client
	myHttpClient = &http.Client{}

	vaultClient, err := api.NewClient(&api.Config{
		HttpClient: vault.WrapHTTPTransport(myHttpClient),
		Address:    "http://vault.mydomain.com:8200",
	})
	return vaultClient, err
}

// Options can be passed in to configure the tracer
func ExampleWrapHTTPTransport_withOptions() (*api.Client, error) {
	var myHttpClient *http.Client
	myHttpClient = &http.Client{}

	vaultClient, err := api.NewClient(&api.Config{
		HttpClient: vault.WrapHTTPTransport(
			myHttpClient,
			httptrace.RTWithAnalytics(true),
			httptrace.RTWithAnalyticsRate(1.0)),
		Address: "http://vault.mydomain.com:8200",
	})
	return vaultClient, err
}
