// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package span_pointers

import (
	"crypto/sha256"
	"encoding/hex"
	"github.com/aws/smithy-go/middleware"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

const (
	// SpanPointerHashLengthBytes 16 bytes = 32 chars.
	// See https://github.com/DataDog/dd-span-pointer-rules/blob/main/README.md#general-hashing-rules
	SpanPointerHashLengthBytes = 16
	PointerDownDirection       = "d"
	LinkKind                   = "span-pointer"
	S3PointerKind              = "aws.s3.object"
)

func AddSpanPointers(serviceId string, in middleware.DeserializeInput, out middleware.DeserializeOutput, span tracer.Span) {
	switch serviceId {
	case "S3":
		//handleS3Operation(in, out, span);
	}
}

// generatePointerHash generates a unique hash from an array of strings by joining them with | before hashing.
// Used to uniquely identify AWS requests for span pointers.
// Returns a 32-character hash uniquely identifying the components.
func generatePointerHash(components []string) string {
	h := sha256.New()
	for i, component := range components {
		if i > 0 {
			h.Write([]byte("|"))
		}
		h.Write([]byte(component))
	}

	fullHash := h.Sum(nil)
	return hex.EncodeToString(fullHash[:SpanPointerHashLengthBytes])
}
