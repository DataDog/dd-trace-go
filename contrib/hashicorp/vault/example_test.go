// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.

package vault_test

import (
	"fmt"
	"log"
	"net/http"

	vaulttrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/hashicorp/vault"

	"github.com/hashicorp/vault/api"
)

// This is the most basic way to enable tracing with Vault.
func ExampleNewHTTPClient() {
	c, err := api.NewClient(&api.Config{
		HttpClient: vaulttrace.NewHTTPClient(),
		Address:    "http://vault.mydomain.com:8200",
	})
	if err != nil {
		log.Fatalf("Failed to create Vault client: %s\n", err)
	}
	// This call wil be traced
	c.Logical().Read("/secret/key")
}

// NewHTTPClient can be called with additional options for further configuration.
func ExampleNewHTTPClient_withOptions() {
	c, err := api.NewClient(&api.Config{
		HttpClient: vaulttrace.NewHTTPClient(
			vaulttrace.WithServiceName("my.vault"),
			vaulttrace.WithAnalytics(true),
		),
		Address: "http://vault.mydomain.com:8200",
	})
	if err != nil {
		log.Fatalf("Failed to create Vault client: %s\n", err)
	}
	// This call wil be traced
	c.Logical().Read("/secret/key")
}

// If you already have an http.Client that you're using, you can add tracing to it
// with WrapHTTPClient.
func ExampleWrapHTTPClient() {
	// We use a custom *http.Client to talk to Vault.
	c := &http.Client{
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			if len(via) > 5 {
				return fmt.Errorf("Won't perform more that 5 redirects.")
			}
			return nil
		},
	}
	client, err := api.NewClient(&api.Config{
		HttpClient: vaulttrace.WrapHTTPClient(c),
		Address:    "http://vault.mydomain.com:8200",
	})
	if err != nil {
		log.Fatalf("Failed to create Vault client: %s\n", err)
	}

	// This call wil be traced
	client.Logical().Read("/secret/key")
}

// WrapHTTPClient can be called with additional options to configure the integration.
func ExampleWrapHTTPClient_withOptions() {
	// We use a custom *http.Client to talk to Vault.
	c := &http.Client{
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			if len(via) > 5 {
				return fmt.Errorf("Won't perform more that 5 redirects.")
			}
			return nil
		},
	}
	client, err := api.NewClient(&api.Config{
		HttpClient: vaulttrace.WrapHTTPClient(
			c,
			vaulttrace.WithServiceName("my.vault"),
			vaulttrace.WithAnalytics(true),
		),
		Address: "http://vault.mydomain.com:8200",
	})
	if err != nil {
		log.Fatalf("Failed to create Vault client: %s\n", err)
	}
	// This call wil be traced
	client.Logical().Read("/secret/key")
}
