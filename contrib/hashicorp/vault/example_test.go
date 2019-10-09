package vault_test

import (
	"fmt"
	"log"
	"net/http"

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/hashicorp/vault"

	"github.com/hashicorp/vault/api"
)

// This is the most basic way to enable tracing with Vault
func ExampleNewHTTPClient() {
	c, err := api.NewClient(&api.Config{
		HttpClient: vault.NewHTTPClient(),
		Address:    "http://vault.mydomain.com:8200",
	})
	if err != nil {
		log.Fatalf("Failed to create vault client: %s\n", err)
	}

	// This call wil be traced
	c.Logical().Read("/secret/key")
}

// Options can be passed in to configure the tracer
func ExampleNewHTTPClient_withOptions() {
	c, err := api.NewClient(&api.Config{
		HttpClient: vault.NewHTTPClient(
			vault.WithServiceName("my.vault"),
			vault.WithAnalytics(true),
			vault.WithAnalyticsRate(1.0)),
		Address: "http://vault.mydomain.com:8200",
	})
	if err != nil {
		log.Fatalf("Failed to create vault client: %s\n", err)
	}

	// This call wil be traced
	c.Logical().Read("/secret/key")
}

// If you already have an *http.Client that you're using (c in this example), you can continue to use it by
// calling WrapHTTPClient to wrap the tracing code around its Transport.
func ExampleWrapHTTPClient() {
	// We use a custom *http.Client to talk to vault.
	c := &http.Client{
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			if len(via) > 5 {
				return fmt.Errorf("Won't perform more that 5 redirects.")
			}
			return nil
		},
	}

	client, err := api.NewClient(&api.Config{
		HttpClient: vault.WrapHTTPClient(c),
		Address:    "http://vault.mydomain.com:8200",
	})
	if err != nil {
		log.Fatalf("Failed to create vault client: %s\n", err)
	}

	// This call wil be traced
	client.Logical().Read("/secret/key")
}

// Options can be passed in to configure the tracer
func ExampleWrapHTTPClient_withOptions() {
	// We use a custom *http.Client to talk to vault.
	c := &http.Client{
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			if len(via) > 5 {
				return fmt.Errorf("Won't perform more that 5 redirects.")
			}
			return nil
		},
	}

	client, err := api.NewClient(&api.Config{
		HttpClient: vault.WrapHTTPClient(
			c,
			vault.WithServiceName("my.vault"),
			vault.WithAnalytics(true),
			vault.WithAnalyticsRate(1.0)),
		Address: "http://vault.mydomain.com:8200",
	})
	if err != nil {
		log.Fatalf("Failed to create vault client: %s\n", err)
	}

	// This call wil be traced
	client.Logical().Read("/secret/key")
}
