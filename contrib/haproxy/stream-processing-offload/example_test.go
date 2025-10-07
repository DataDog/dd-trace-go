// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package streamprocessingoffload

import (
	"context"
	"log"
	"net"

	"github.com/negasus/haproxy-spoe-go/agent"
	"github.com/negasus/haproxy-spoe-go/logger"
)

func Example_server() {
	// Create a listener for the server.
	ln, err := net.Listen("tcp4", "127.0.0.1:3000")
	if err != nil {
		log.Fatal(err)
	}

	// Initialize the SPOA agent server with the configuration
	appsecHAProxy := NewHAProxySPOA(AppsecHAProxyConfig{
		BlockingUnavailable:  false,
		BodyParsingSizeLimit: 1000000, // 1MB
		Context:              context.Background(),
	})

	a := agent.New(appsecHAProxy.Handler, logger.NewDefaultLog())

	// Start serving incoming connections.
	if err := a.Serve(ln); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}
