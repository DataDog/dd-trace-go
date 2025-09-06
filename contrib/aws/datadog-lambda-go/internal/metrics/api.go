/*
 * Unless explicitly stated otherwise all files in this repository are licensed
 * under the Apache License Version 2.0.
 *
 * This product includes software developed at Datadog (https://www.datadoghq.com/).
 * Copyright 2021 Datadog, Inc.
 */

package metrics

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/DataDog/dd-trace-go/v2/contrib/aws/datadog-lambda-go/internal/logger"
)

type (
	// Client sends metrics to Datadog
	Client interface {
		SendMetrics(metrics []APIMetric) error
	}

	// APIClient send metrics to Datadog, via the Datadog API
	APIClient struct {
		apiKey            string
		apiKeyDecryptChan <-chan string
		baseAPIURL        string
		httpClient        *http.Client
		context           context.Context
	}

	// APIClientOptions contains instantiation options from creating an APIClient.
	APIClientOptions struct {
		baseAPIURL        string
		apiKey            string
		kmsAPIKey         string
		decrypter         Decrypter
		httpClientTimeout time.Duration
	}

	postMetricsModel struct {
		Series []APIMetric `json:"series"`
	}
)

// MakeAPIClient creates a new API client with the given api and app keys
func MakeAPIClient(ctx context.Context, options APIClientOptions) *APIClient {
	httpClient := &http.Client{
		Timeout: options.httpClientTimeout,
	}
	client := &APIClient{
		apiKey:     options.apiKey,
		baseAPIURL: options.baseAPIURL,
		httpClient: httpClient,
		context:    ctx,
	}
	if len(options.apiKey) == 0 && len(options.kmsAPIKey) != 0 {
		client.apiKeyDecryptChan = client.decryptAPIKey(options.decrypter, options.kmsAPIKey)
	}

	return client
}

// SendMetrics posts a batch metrics payload to the Datadog API
func (cl *APIClient) SendMetrics(metrics []APIMetric) error {

	// If the api key was provided as a kms key, wait for it to finish decrypting
	if cl.apiKeyDecryptChan != nil {
		cl.apiKey = <-cl.apiKeyDecryptChan
		cl.apiKeyDecryptChan = nil
	}

	content, err := marshalAPIMetricsModel(metrics)
	if err != nil {
		return fmt.Errorf("Couldn't marshal metrics model: %v", err)
	}
	body := bytes.NewBuffer(content)

	// For the moment we only support distribution metrics.
	// Other metric types use the "series" endpoint, which takes an identical payload.
	req, err := http.NewRequest("POST", cl.makeRoute("distribution_points"), body)
	if err != nil {
		return fmt.Errorf("Couldn't create send metrics request:%v", err)
	}
	req = req.WithContext(cl.context)

	defer req.Body.Close()

	logger.Debug(fmt.Sprintf("Sending payload with body %s", content))

	cl.addAPICredentials(req)

	resp, err := cl.httpClient.Do(req)

	if err != nil {
		return fmt.Errorf("Failed to send metrics to API")
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		if resp.StatusCode == 403 {
			logger.Debug(fmt.Sprintf("authorization failed with api key of length %d characters", len(cl.apiKey)))
		}
		bodyBytes, err := io.ReadAll(resp.Body)
		body := ""
		if err == nil {
			body = string(bodyBytes)
		}
		return fmt.Errorf("Failed to send metrics to API. Status Code %d, Body %s", resp.StatusCode, body)
	}

	return err
}

func (cl *APIClient) decryptAPIKey(decrypter Decrypter, kmsAPIKey string) <-chan string {

	ch := make(chan string)

	go func() {
		result, err := decrypter.Decrypt(kmsAPIKey)
		if err != nil {
			logger.Error(fmt.Errorf("Couldn't decrypt api kms key %s", err))
		}
		ch <- result
		close(ch)
	}()
	return ch
}

func (cl *APIClient) addAPICredentials(req *http.Request) {
	query := req.URL.Query()
	query.Add(apiKeyParam, cl.apiKey)
	req.URL.RawQuery = query.Encode()
}

func (cl *APIClient) makeRoute(route string) string {
	url := fmt.Sprintf("%s/%s", cl.baseAPIURL, route)
	logger.Debug(fmt.Sprintf("posting to url %s", url))
	return url
}

func marshalAPIMetricsModel(metrics []APIMetric) ([]byte, error) {
	pm := postMetricsModel{}
	pm.Series = metrics
	return json.Marshal(pm)
}
