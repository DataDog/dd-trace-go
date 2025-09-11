// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package metrics

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"

	"github.com/DataDog/dd-trace-go/v2/contrib/aws/datadog-lambda-go/internal/logger"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/kms"
)

type (
	// Decrypter attempts to decrypt a key
	Decrypter interface {
		Decrypt(cipherText string) (string, error)
	}

	kmsDecrypter struct {
		kmsClient *kms.Client
	}

	clientDecrypter interface {
		Decrypt(context.Context, *kms.DecryptInput, ...func(*kms.Options)) (*kms.DecryptOutput, error)
	}
)

// functionNameEnvVar is the environment variable that stores the Lambda function name
const functionNameEnvVar string = "AWS_LAMBDA_FUNCTION_NAME"

// encryptionContextKey is the key added to the encryption context by the Lambda console UI
const encryptionContextKey string = "LambdaFunctionName"

// MakeKMSDecrypter creates a new decrypter which uses the AWS KMS service to decrypt variables
func MakeKMSDecrypter(fipsMode bool) Decrypter {
	fipsEndpoint := aws.FIPSEndpointStateUnset
	if fipsMode {
		fipsEndpoint = aws.FIPSEndpointStateEnabled
		logger.Debug("Using FIPS endpoint for KMS decryption.")
	}

	cfg, err := config.LoadDefaultConfig(context.Background(), config.WithUseFIPSEndpoint(fipsEndpoint))
	if err != nil {
		logger.Error(fmt.Errorf("could not create a new aws config: %v", err))
		panic(err)
	}
	return &kmsDecrypter{
		kmsClient: kms.NewFromConfig(cfg),
	}
}

func (kd *kmsDecrypter) Decrypt(ciphertext string) (string, error) {
	return decryptKMS(kd.kmsClient, ciphertext)
}

// decryptKMS decodes and deciphers the base64-encoded ciphertext given as a parameter using KMS.
// For this to work properly, the Lambda function must have the appropriate IAM permissions.
func decryptKMS(kmsClient clientDecrypter, ciphertext string) (string, error) {
	decodedBytes, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", fmt.Errorf("failed to encode cipher text to base64: %v", err)
	}

	// When the API key is encrypted using the AWS console, the function name is added as an
	// encryption context. When the API key is encrypted using the AWS CLI, no encryption context
	// is added. We need to try decrypting the API key both with and without the encryption context.

	// Try without encryption context, in case API key was encrypted using the AWS CLI
	functionName := os.Getenv(functionNameEnvVar)
	params := &kms.DecryptInput{
		CiphertextBlob: decodedBytes,
	}
	ctx := context.Background()
	response, err := kmsClient.Decrypt(ctx, params)

	if err != nil {
		logger.Debug("Failed to decrypt ciphertext without encryption context, retrying with encryption context")
		// Try with encryption context, in case API key was encrypted using the AWS Console
		params = &kms.DecryptInput{
			CiphertextBlob: decodedBytes,
			EncryptionContext: map[string]string{
				encryptionContextKey: functionName,
			},
		}
		response, err = kmsClient.Decrypt(ctx, params)
		if err != nil {
			return "", fmt.Errorf("failed to decrypt ciphertext with kms: %v", err)
		}
	}

	plaintext := string(response.Plaintext)
	return plaintext, nil
}
