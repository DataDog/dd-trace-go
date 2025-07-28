// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package main

import (
	"net"
	"os"

	"github.com/negasus/haproxy-spoe-go/agent"
	"github.com/negasus/haproxy-spoe-go/logger"

	"github.com/DataDog/dd-trace-go/contrib/haproxy/v2"
)

var log = NewLogger()

func main() {
	log.Info("datadog_haproxy_spoa: starting\n")

	listener, err := net.Listen("tcp4", "127.0.0.1:3000")
	if err != nil {
		log.Error("datadog_haproxy_spoa: error create listener, %v", err)
		os.Exit(1)
	}
	defer listener.Close()

	a := agent.New(haproxy.Handler, logger.NewDefaultLog())

	if err := a.Serve(listener); err != nil {
		log.Printf("error agent serve: %+v\n", err)
	}
}
