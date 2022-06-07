// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package httptrace

import "os"

type config struct {
	ipHeader string
}

var (
	clientIPHeaderEnvVar = "DD_CLIENT_IP_HEADER"
	cfg                  = newConfig()
)

func newConfig() *config {
	return &config{
		ipHeader: os.Getenv(clientIPHeaderEnvVar),
	}
}
