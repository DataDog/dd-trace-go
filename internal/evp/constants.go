// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package evp

const (
	// PayloadSizeLimit is the EVP uncompressed request-body limit.
	PayloadSizeLimit = 5 * 1024 * 1024

	// EventSizeLimit is the default individual EVP event-size limit used by product writers
	// that need a local cap before request batching.
	EventSizeLimit = 5_000_000
)
