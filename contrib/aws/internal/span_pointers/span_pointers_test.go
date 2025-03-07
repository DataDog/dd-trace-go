// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package spanpointers

import (
	"context"
	awsmiddleware "github.com/aws/aws-sdk-go-v2/aws/middleware"
	"github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"net/http"
	"net/url"
	"testing"
)

func TestGeneratePointerHash(t *testing.T) {
	tests := []struct {
		name         string
		components   []string
		expectedHash string
	}{
		// S3 Tests
		{
			name: "s3 basic values",
			components: []string{
				"some-bucket",
				"some-key.data",
				"ab12ef34",
			},
			expectedHash: "e721375466d4116ab551213fdea08413",
		},
		{
			name: "s3 non-ascii key",
			components: []string{
				"some-bucket",
				"some-key.你好",
				"ab12ef34",
			},
			expectedHash: "d1333a04b9928ab462b5c6cadfa401f4",
		},
		{
			name: "s3 multipart-upload",
			components: []string{
				"some-bucket",
				"some-key.data",
				"ab12ef34-5",
			},
			expectedHash: "2b90dffc37ebc7bc610152c3dc72af9f",
		},
		// DynamoDB tests
		{
			name: "dynamodb one string primary key",
			components: []string{
				"some-table",
				"some-key",
				"some-value",
				"",
				"",
			},
			expectedHash: "7f1aee721472bcb48701d45c7c7f7821",
		},
		{
			name: "dynamodb one number primary key",
			components: []string{
				"some-table",
				"some-key",
				"123.456",
				"",
				"",
			},
			expectedHash: "434a6dba3997ce4dbbadc98d87a0cc24",
		},
		{
			name: "dynamodb string and number primary key",
			components: []string{
				"some-table",
				"other-key",
				"123",
				"some-key",
				"some-value",
			},
			expectedHash: "7aa1b80b0e49bd2078a5453399f4dd67",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := generatePointerHash(tt.components)
			if got != tt.expectedHash {
				t.Errorf("GeneratePointerHash() = %v, want %v", got, tt.expectedHash)
			}
		})
	}
}

func TestHandleS3Operation(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	tests := []struct {
		name          string
		bucket        string
		key           string
		etag          string
		expectedHash  string
		expectSuccess bool
	}{
		{
			name:          "basic operation",
			bucket:        "some-bucket",
			key:           "some-key.data",
			etag:          "ab12ef34",
			expectedHash:  "e721375466d4116ab551213fdea08413",
			expectSuccess: true,
		},
		{
			name:          "quoted etag",
			bucket:        "some-bucket",
			key:           "some-key.data",
			etag:          "\"ab12ef34\"",
			expectedHash:  "e721375466d4116ab551213fdea08413",
			expectSuccess: true,
		},
		{
			name:          "non-ascii key",
			bucket:        "some-bucket",
			key:           "some-key.你好",
			etag:          "ab12ef34",
			expectedHash:  "d1333a04b9928ab462b5c6cadfa401f4",
			expectSuccess: true,
		},
		{
			name:          "empty bucket",
			bucket:        "",
			key:           "some_key",
			etag:          "some_etag",
			expectSuccess: false,
		},
		{
			name:          "empty key",
			bucket:        "some_bucket",
			key:           "",
			etag:          "some_etag",
			expectSuccess: false,
		},
		{
			name:          "empty etag",
			bucket:        "some_bucket",
			key:           "some_key",
			etag:          "",
			expectSuccess: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			span, ctx := tracer.StartSpanFromContext(ctx, "test.s3.operation")
			ctx = awsmiddleware.SetServiceID(ctx, "S3")

			// Create request
			reqURL, _ := url.Parse("https://" + tt.bucket + ".s3.region.amazonaws.com/" + tt.key)
			req := &smithyhttp.Request{
				Request: &http.Request{
					URL: reqURL,
				},
			}

			// Create response
			header := http.Header{}
			header.Set("ETag", tt.etag)
			res := &smithyhttp.Response{
				Response: &http.Response{
					Header: header,
				},
			}

			// Create input/output
			in := middleware.DeserializeInput{
				Request: req,
			}
			out := middleware.DeserializeOutput{
				RawResponse: res,
			}

			AddSpanPointers(ctx, in, out, span)
			span.Finish()
			spans := mt.FinishedSpans()
			if tt.expectSuccess {
				require.Len(t, spans, 1)
				links := spans[0].Links()
				assert.NotEmpty(t, links, "Expected span links to not be empty")

				attributes := links[0].Attributes
				assert.Equal(t, S3PointerKind, attributes["ptr.kind"])
				assert.Equal(t, PointerDownDirection, attributes["ptr.dir"])
				assert.Equal(t, LinkKind, attributes["link.kind"])
				assert.Equal(t, tt.expectedHash, attributes["ptr.hash"])
			} else {
				require.Len(t, spans, 1)
				links := spans[0].Links()
				assert.Empty(t, links, "Expected no span links to be set")
			}
			mt.Reset()
		})
	}
}
