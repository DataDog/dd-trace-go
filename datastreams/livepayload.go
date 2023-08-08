// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

//go:generate msgp -unexported -marshal=false -o=livepayload_msgp.go -tests=false
package datastreams

type LivePayload struct {
	Message   []byte
	Topic     string
	Partition int32
	Offset    int64
	TpNanos   int64
}
type LivePayloads struct {
	Payloads      []LivePayload
	Service       string
	TracerVersion string
	TracerLang    string
}
