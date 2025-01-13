package span_pointers

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"
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

func HandleS3Operation(in middleware.DeserializeInput, out middleware.DeserializeOutput, span tracer.Span) {
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
	if key == "" {
		log.Debug("Unable to create S3 span pointer because key could not be found.")
		return
	}

	bucket := strings.Split(req.URL.Host, ".")[0]
	if bucket == "" {
		log.Debug("Unable to create S3 span pointer because bucket could not be found.")
		return
	}

	// the AWS SDK sometimes wraps the eTag in quotes
	etag := strings.Trim(res.Header.Get("ETag"), "\"")
	if etag == "" {
		log.Debug("Unable to create S3 span pointer because eTag could not be found.")
		return
	}

	// Hash calculation rules: https://github.com/DataDog/dd-span-pointer-rules/blob/main/AWS/S3/Object/README.md
	components := []string{bucket, key, etag}
	hash := generatePointerHash(components)
	fmt.Printf("Hash: %s\n", hash)
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
