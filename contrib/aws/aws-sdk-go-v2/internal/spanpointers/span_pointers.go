// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package spanpointers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"

	awsmiddleware "github.com/aws/aws-sdk-go-v2/aws/middleware"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	"github.com/DataDog/dd-trace-go/contrib/aws/aws-sdk-go-v2/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

const (
	// SpanPointerHashLengthBytes 16 bytes = 32 chars.
	// See https://github.com/DataDog/dd-span-pointer-rules/blob/main/README.md#general-hashing-rules
	SpanPointerHashLengthBytes = 16
	PointerDownDirection       = "d"
	LinkKind                   = "span-pointer"
	DynamoDbPointerKind        = "aws.dynamodb.item"
	S3PointerKind              = "aws.s3.object"
)

var separatorBytes = []byte("|")

// DynamoDbTableName is a context key for storing DynamoDB table name
type DynamoDbTableName struct{}

// DynamoDbKeyMap is a context key for storing DynamoDB key map
type DynamoDbKeyMap struct{}

func AddSpanPointers(context context.Context, in middleware.DeserializeInput, out middleware.DeserializeOutput, span *tracer.Span) {
	// TODO(@nhulston) after refactoring S3, in/out can be removed
	serviceID := awsmiddleware.GetServiceID(context)
	switch serviceID {
	case "S3":
		handleS3Operation(in, out, span)
	case "DynamoDB":
		handleDynamoDbOperation(context, span)
	}
}

func SetDynamoDbParamsOnContext(spanctx context.Context, params interface{}) context.Context {
	switch params := params.(type) {
	case *dynamodb.UpdateItemInput:
		spanctx = context.WithValue(spanctx, DynamoDbTableName{}, *params.TableName)
		spanctx = context.WithValue(spanctx, DynamoDbKeyMap{}, params.Key)
	case *dynamodb.DeleteItemInput:
		spanctx = context.WithValue(spanctx, DynamoDbTableName{}, *params.TableName)
		spanctx = context.WithValue(spanctx, DynamoDbKeyMap{}, params.Key)
	}

	return spanctx
}

func handleS3Operation(in middleware.DeserializeInput, out middleware.DeserializeOutput, span *tracer.Span) {
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
		internal.Instr.Logger().Debug("Unable to create S3 span pointer because required fields could not be found.")
		return
	}

	// Hash calculation rules: https://github.com/DataDog/dd-span-pointer-rules/blob/main/AWS/S3/Object/README.md
	components := []string{bucket, key, etag}
	hash := generatePointerHash(components)

	link := tracer.SpanLink{
		// We leave trace_id and span_id as 0.
		// The Datadog frontend will use `ptr.hash` to find the linked span.
		TraceID: 0,
		SpanID:  0,
		Attributes: map[string]string{
			"ptr.kind":  S3PointerKind,
			"ptr.dir":   PointerDownDirection,
			"ptr.hash":  hash,
			"link.kind": LinkKind,
		},
	}

	span.AddLink(link)
}

func handleDynamoDbOperation(ctx context.Context, span *tracer.Span) {
	// Retrieve table name from context
	tableNameVal := ctx.Value(DynamoDbTableName{})
	if tableNameVal == nil {
		// This could be a DynamoDB operation that's not supported by span pointers,
		// so we return without logging anything.
		return
	}
	tableName, ok := tableNameVal.(string)
	if !ok {
		return
	}

	// Retrieve key map from context
	keyMapVal := ctx.Value(DynamoDbKeyMap{})
	if keyMapVal == nil {
		return
	}
	keyMap, ok := keyMapVal.(map[string]types.AttributeValue)
	if !ok || len(keyMap) == 0 {
		return
	}

	// Hash calculation rules: https://github.com/DataDog/dd-span-pointer-rules/blob/main/AWS/DynamoDB/Item/README.md
	var componentsToHash []string

	// Extract and sort the keys
	keys := make([]string, 0, len(keyMap))
	for k := range keyMap {
		keys = append(keys, k)
	}

	if len(keys) == 1 {
		keyName := keys[0]
		keyValue := attributeValueToString(keyMap[keyName])

		componentsToHash = []string{tableName, keyName, keyValue, "", ""}
	} else {
		sort.Strings(keys)
		key1 := keys[0]
		key2 := keys[1]
		value1 := attributeValueToString(keyMap[key1])
		value2 := attributeValueToString(keyMap[key2])

		componentsToHash = []string{tableName, key1, value1, key2, value2}
	}

	hash := generatePointerHash(componentsToHash)
	link := tracer.SpanLink{
		TraceID: 0,
		SpanID:  0,
		Attributes: map[string]string{
			"ptr.kind":  DynamoDbPointerKind,
			"ptr.dir":   PointerDownDirection,
			"ptr.hash":  hash,
			"link.kind": LinkKind,
		},
	}

	span.AddLink(link)
}

// DynamoDb values can only be string, number, or binary
func attributeValueToString(attr types.AttributeValue) string {
	switch v := attr.(type) {
	case *types.AttributeValueMemberS:
		return v.Value
	case *types.AttributeValueMemberN:
		return v.Value
	case *types.AttributeValueMemberB:
		// Convert binary data to string using UTF-8 encoding (Go's default)
		return string(v.Value)
	default:
		return ""
	}
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
