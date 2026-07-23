// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package evp

const (
	// PayloadSizeLimit is the Agent EVP proxy uncompressed request-body limit.
	// Source of truth: datadog-agent/pkg/config/setup/apm.go sets
	// evp_proxy_config.max_payload_size to int64(10*1024*1024), and
	// datadog-agent/pkg/trace/config/config.go defaults EVPProxy.MaxPayloadSize to
	// 10 * 1024 * 1024.
	PayloadSizeLimit = 10 * 1024 * 1024
)
