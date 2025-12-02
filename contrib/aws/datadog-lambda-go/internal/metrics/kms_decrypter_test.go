// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package metrics

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/stretchr/testify/assert"
)

// mockEncryptedAPIKeyBase64 represents an API key encrypted with KMS and encoded as a base64 string
const mockEncryptedAPIKeyBase64 = "MjIyMjIyMjIyMjIyMjIyMg=="

// mockDecodedEncryptedAPIKey represents the encrypted API key after it has been decoded from base64
const mockDecodedEncryptedAPIKey = "2222222222222222"

// expectedDecryptedAPIKey represents the true value of the API key after decryption by KMS
const expectedDecryptedAPIKey = "1111111111111111"

// mockFunctionName represents the name of the current function
var mockFunctionName = "my-Function"

type mockKMSClientWithEncryptionContext struct{}

func (mockKMSClientWithEncryptionContext) Decrypt(_ context.Context, params *kms.DecryptInput, _ ...func(*kms.Options)) (*kms.DecryptOutput, error) {
	encryptionContextPointer, exists := params.EncryptionContext[encryptionContextKey]
	if !exists {
		return nil, errors.New("InvalidCiphertextException")
	}
	if encryptionContextPointer != mockFunctionName {
		return nil, errors.New("InvalidCiphertextException")
	}
	if bytes.Equal(params.CiphertextBlob, []byte(mockDecodedEncryptedAPIKey)) {
		return &kms.DecryptOutput{
			Plaintext: []byte(expectedDecryptedAPIKey),
		}, nil
	}
	return nil, errors.New("KMS error")
}

type mockKMSClientNoEncryptionContext struct{}

func (mockKMSClientNoEncryptionContext) Decrypt(_ context.Context, params *kms.DecryptInput, _ ...func(*kms.Options)) (*kms.DecryptOutput, error) {
	if params.EncryptionContext[encryptionContextKey] != "" {
		return nil, errors.New("InvalidCiphertextException")
	}
	if bytes.Equal(params.CiphertextBlob, []byte(mockDecodedEncryptedAPIKey)) {
		return &kms.DecryptOutput{
			Plaintext: []byte(expectedDecryptedAPIKey),
		}, nil
	}
	return nil, errors.New("KMS error")
}

func TestDecryptKMSWithEncryptionContext(t *testing.T) {
	os.Setenv(functionNameEnvVar, mockFunctionName)
	defer os.Setenv(functionNameEnvVar, "")

	client := mockKMSClientWithEncryptionContext{}
	result, _ := decryptKMS(client, mockEncryptedAPIKeyBase64)
	assert.Equal(t, expectedDecryptedAPIKey, result)
}

func TestDecryptKMSNoEncryptionContext(t *testing.T) {
	client := mockKMSClientNoEncryptionContext{}
	result, _ := decryptKMS(client, mockEncryptedAPIKeyBase64)
	assert.Equal(t, expectedDecryptedAPIKey, result)
}
