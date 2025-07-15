// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package go_control_plane

import (
	v2 "github.com/DataDog/dd-trace-go/contrib/envoyproxy/go-control-plane/v2"

	envoyextproc "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
)

// AppsecEnvoyExternalProcessorServer creates and returns a new instance of appsecEnvoyExternalProcessorServer.
func AppsecEnvoyExternalProcessorServer(userImplementation envoyextproc.ExternalProcessorServer) envoyextproc.ExternalProcessorServer {
	return v2.AppsecEnvoyExternalProcessorServer(userImplementation, v2.AppsecEnvoyConfig{})
}
