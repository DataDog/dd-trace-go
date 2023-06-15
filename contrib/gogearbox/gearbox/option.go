// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package gearbox

import (
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
)

type config struct {
	serviceName string
}

func newConfig(service string) *config {
	if service == "" {
		service = "gearbox.router"
		if svc := globalconfig.ServiceName(); svc != "" {
			service = svc
		}
	}
	
	return &config{
		serviceName: service,
	}
}