// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package spanpointers

import (
	"crypto/sha256"
	"encoding/hex"
	"github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"strings"
)

const (
	// SpanPointerHashLengthBytes 16 bytes = 32 chars.
	// See https://github.com/DataDog/dd-span-pointer-rules/blob/main/README.md#general-hashing-rules
	SpanPointerHashLengthBytes = 16
	PointerDownDirection       = "d"
	LinkKind                   = "span-pointer"
	S3PointerKind              = "aws.s3.object"
)

var separatorBytes = []byte("|")

func AddSpanPointers(serviceID string, in middleware.DeserializeInput, out middleware.DeserializeOutput, span tracer.Span) {
	switch serviceID {
	case "S3":
		handleS3Operation(in, out, span)
	}
}

func handleS3Operation(in middleware.DeserializeInput, out middleware.DeserializeOutput, span tracer.Span) {
	spanWithLinks, ok := span.(tracer.SpanWithLinks)
	if !ok {
		return
	}

	req, ok := in.Request.(*smithyhttp.Request)
	if !ok {
		return
	}
	res, ok := out.RawResponse.(*smithyhttp.Response)
	if !ok {
		return
	}

	// URL format: https://BUCKETNAME.s3.REGION.amazonaws.com/KEYNAME?x-id=OPERATIONNAME
	key := strings.TrimPrefix(req.URL.Path, "/")
	bucket := strings.Split(req.URL.Host, ".")[0]
	// the AWS SDK sometimes wraps the eTag in quotes
	etag := strings.Trim(res.Header.Get("ETag"), "\"")
	if key == "" || bucket == "" || etag == "" {
		log.Debug("Unable to create S3 span pointer because required fields could not be found.")
		return
	}

	// Hash calculation rules: https://github.com/DataDog/dd-span-pointer-rules/blob/main/AWS/S3/Object/README.md
	components := []string{bucket, key, etag}
	hash := generatePointerHash(components)

	link := ddtrace.SpanLink{
		// We leave trace_id, span_id, trade_id_high, tracestate, and flags as 0 or empty.
		// The Datadog frontend will use `ptr.hash` to find the linked span.
		TraceID:     0,
		SpanID:      0,
		TraceIDHigh: 0,
		Flags:       0,
		Tracestate:  "",
		Attributes: map[string]string{
			"ptr.kind":  S3PointerKind,
			"ptr.dir":   PointerDownDirection,
			"ptr.hash":  hash,
			"link.kind": LinkKind,
		},
	}

	spanWithLinks.AddSpanLink(link)
}

// generatePointerHash generates a unique hash from an array of strings by joining them with | before hashing.
// Used to uniquely identify AWS requests for span pointers.
// Returns a 32-character hash uniquely identifying the components.
func generatePointerHash(components []string) string {
	h := sha256.New()
	for i, component := range components {
		if i > 0 {
			h.Write(separatorBytes)
		}
		h.Write([]byte(component))
	}

	fullHash := h.Sum(nil)
	return hex.EncodeToString(fullHash[:SpanPointerHashLengthBytes])
}
