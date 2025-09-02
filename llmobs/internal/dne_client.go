// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package internal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/cenkalti/backoff/v5"
)

const (
	headerEVPSubdomain   = "X-Datadog-EVP-Subdomain"
	headerRateLimitReset = "x-ratelimit-reset"
)

const (
	endpointEvalMetric = "/api/intake/llm-obs/v2/eval-metric"
	endpointLLMSpan    = "/api/v2/llmobs"

	subdomainLLMSpan    = "llmobs-intake"
	subdomainEvalMetric = "api"
	subdomainDNE        = "api"
)

const (
	basePathEVPProxy = "/evp_proxy/v2"
	basePathDNE      = "/api/unstable/llm-obs/v1"
)

const (
	defaultSite    = "datadoghq.com"
	defaultTimeout = 5 * time.Second
	// DefaultMaxRetries is the default number of retries for a request.
	defaultMaxRetries uint = 3
	// DefaultBackoff is the default backoff time for a request.
	defaultBackoff time.Duration = 100 * time.Millisecond
)

const (
	resourceTypeDatasets    = "datasets"
	resourceTypeExperiments = "experiments"
	resourceTypeProjects    = "projects"
)

// We copy the transport to avoid using the default one, as it might be
// augmented with tracing and we don't want these calls to be recorded.
// See https://golang.org/pkg/net/http/#DefaultTransport .
var defaultHTTPClient = &http.Client{
	Transport: &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	},
	Timeout: defaultTimeout,
}

// DNEClient sends requests to the LLMObs “experiments” API set like the Python client.
type DNEClient struct {
	httpClient     *http.Client
	defaultHeaders map[string]string
	baseURL        string
	agentless      bool
}

// NewDNEClient builds a client configured like the Python one.
func newDNEClient(cfg *Config) *DNEClient {
	site := defaultSite
	if cfg.Site != "" {
		site = cfg.Site
	}

	baseURL := ""
	defaultHeaders := map[string]string{
		"Content-Type": "application/json",
	}
	httpClient := defaultHTTPClient

	if cfg.AgentlessEnabled {
		defaultHeaders["DD-API-KEY"] = cfg.APIKey
		if cfg.APPKey != "" {
			defaultHeaders["DD-APPLICATION-KEY"] = cfg.APPKey
		}
		baseURL = fmt.Sprintf("https://%s.%s", subdomainDNE, site)
	} else {
		defaultHeaders[headerEVPSubdomain] = subdomainDNE

		if cfg.AgentURL.Scheme == "unix" {
			dialer := &net.Dialer{
				Timeout:   30 * time.Second,
				KeepAlive: 30 * time.Second,
			}
			httpClient = &http.Client{
				Transport: &http.Transport{
					Proxy: http.ProxyFromEnvironment,
					DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
						return dialer.DialContext(ctx, "unix", (&net.UnixAddr{
							Name: cfg.AgentURL.Path,
							Net:  "unix",
						}).String())
					},
					MaxIdleConns:          100,
					IdleConnTimeout:       90 * time.Second,
					TLSHandshakeTimeout:   10 * time.Second,
					ExpectContinueTimeout: 1 * time.Second,
				},
				Timeout: 10 * time.Second,
			}
			baseURL = internal.UnixDataSocketURL(cfg.AgentURL.Path).String()
		}
	}

	return &DNEClient{
		httpClient:     httpClient,
		defaultHeaders: defaultHeaders,
		baseURL:        baseURL,
		agentless:      cfg.AgentlessEnabled,
	}
}

// DatasetCreate -> POST /datasets
func (c *DNEClient) DatasetCreate(ctx context.Context, name, description string) (*DatasetView, error) {
	path := basePathDNE + "/datasets"
	method := http.MethodPost
	body := RequestDatasetCreate{
		Data: RequestData[DatasetCreate]{
			Type: resourceTypeDatasets,
			Attributes: DatasetCreate{
				Name:        name,
				Description: description,
			},
		},
	}

	status, b, err := c.request(ctx, method, path, body)
	if err != nil {
		return nil, fmt.Errorf("create dataset %q failed: %v (status=%d, body=%s)", name, err, status, string(b))
	}

	var resp ResponseDatasetCreate
	if err := json.Unmarshal(b, &resp); err != nil {
		return nil, fmt.Errorf("decode create dataset response: %w", err)
	}
	id := resp.Data.ID
	dataset := resp.Data.Attributes
	dataset.ID = id
	return &dataset, nil
}

// DatasetDelete -> POST /datasets/delete
func (c *DNEClient) DatasetDelete(ctx context.Context, datasetIDs ...string) error {
	path := basePathDNE + "/datasets/delete"
	method := http.MethodPost
	body := RequestDatasetDelete{
		Data: RequestData[RequestAttributesDatasetDelete]{
			Type: resourceTypeDatasets,
			Attributes: RequestAttributesDatasetDelete{
				DatasetIDs: datasetIDs,
			},
		},
	}

	status, b, err := c.request(ctx, method, path, body)
	if err != nil || status != http.StatusOK {
		return fmt.Errorf("delete dataset %v failed: %v (status=%d, body=%s)", datasetIDs, err, status, string(b))
	}
	return nil
}

// DatasetBatchUpdateRecords -> POST /datasets/{id}/batch_update
func (c *DNEClient) DatasetBatchUpdateRecords(
	ctx context.Context,
	datasetID string,
	insert []DatasetRecordCreate,
	update []DatasetRecordUpdate,
	delete []string,
) (int, []string, error) {
	path := fmt.Sprintf("%s/datasets/%s/batch_update", basePathDNE, url.PathEscape(datasetID))
	method := http.MethodPost
	body := RequestDatasetBatchUpdate{
		Data: RequestData[RequestAttributesDatasetBatchUpdate]{
			Type: resourceTypeDatasets,
			Attributes: RequestAttributesDatasetBatchUpdate{
				InsertRecords: insert,
				UpdateRecords: update,
				DeleteRecords: delete,
			},
		},
	}

	status, b, err := c.request(ctx, method, path, body)
	if err != nil || status != http.StatusOK {
		return -1, nil, fmt.Errorf("batch_update for dataset %q failed: %v (status=%d, body=%s)", datasetID, err, status, string(b))
	}

	var resp ResponseDatasetBatchUpdate
	if err := json.Unmarshal(b, &resp); err != nil {
		return -1, nil, fmt.Errorf("decode batch_update response: %w", err)
	}

	// FIXME: we don't get version numbers in responses to deletion requests
	// TODO(rarguelloF): clarify this part
	var version = -1
	var ids []string
	if len(resp.Data) > 0 {
		if resp.Data[0].Attributes.Version > 0 {
			version = resp.Data[0].Attributes.Version
		}
		for _, d := range resp.Data {
			if d.ID != "" {
				ids = append(ids, d.ID)
			}
		}
	}
	return version, ids, nil
}

// DatasetGetWithRecords -> GET /datasets?filter[name]=... , then GET /datasets/{id}/records
func (c *DNEClient) DatasetGetWithRecords(ctx context.Context, name string) (*DatasetView, []DatasetRecordView, error) {
	// 1) Fetch dataset

	q := url.Values{}
	q.Set("filter[name]", name)
	datasetPath := basePathDNE + "/datasets" + "?" + q.Encode()
	method := http.MethodGet

	status, b, err := c.request(ctx, method, datasetPath, nil)
	if err != nil || status != http.StatusOK {
		return nil, nil, fmt.Errorf("get dataset by name %q failed: %v (status=%d, body=%s)", name, err, status, string(b))
	}

	var datasetResp ResponseDatasetGet
	if err := json.Unmarshal(b, &datasetResp); err != nil {
		return nil, nil, fmt.Errorf("decode datasets list: %w", err)
	}
	if len(datasetResp.Data) == 0 {
		return nil, nil, fmt.Errorf("dataset %q not found", name)
	}
	datasetID := datasetResp.Data[0].ID

	// 2) Fetch records
	recordsPath := fmt.Sprintf("%s/datasets/%s/records", basePathDNE, url.PathEscape(datasetID))
	status, b, err = c.request(ctx, method, recordsPath, nil)
	if err != nil || status != http.StatusOK {
		return nil, nil, fmt.Errorf("get dataset %q records failed: %v (status=%d, body=%s)", name, err, status, string(b))
	}

	var recordsResp ResponseDatasetGetRecords
	if err := json.Unmarshal(b, &recordsResp); err != nil {
		return nil, nil, fmt.Errorf("decode dataset records: %w", err)
	}

	records := make([]DatasetRecordView, 0, len(recordsResp.Data))
	for _, r := range recordsResp.Data {
		rec := r.Attributes
		rec.ID = r.ID
		records = append(records, rec)
	}
	dataset := datasetResp.Data[0].Attributes
	dataset.ID = datasetID

	return &dataset, records, nil
}

// ProjectGetOrCreate -> POST /projects
func (c *DNEClient) ProjectGetOrCreate(ctx context.Context, name string) (*ProjectView, error) {
	path := basePathDNE + "/projects"
	method := http.MethodPost

	body := RequestProjectCreate{
		Data: RequestData[RequestAttributesProjectCreate]{
			Type: resourceTypeProjects,
			Attributes: RequestAttributesProjectCreate{
				Name:        name,
				Description: "",
			},
		},
	}
	status, b, err := c.request(ctx, method, path, body)
	if err != nil || status != http.StatusOK {
		return nil, fmt.Errorf("create project %q failed: %v (status=%d, body=%s)", name, err, status, string(b))
	}

	var resp ResponseProjectCreate
	if err := json.Unmarshal(b, &resp); err != nil {
		return nil, fmt.Errorf("decode project response: %w", err)
	}

	project := resp.Data.Attributes
	project.ID = resp.Data.ID
	return &project, nil
}

// ExperimentCreate -> POST /experiments
func (c *DNEClient) ExperimentCreate(
	ctx context.Context,
	name, datasetID, projectID string,
	datasetVersion int,
	expConfig map[string]any,
	tags []string,
	description string, //TODO: change to use RequestAttributesExperimentCreate
) (*ExperimentView, error) {
	path := basePathDNE + "/experiments"
	method := http.MethodPost

	if expConfig == nil {
		expConfig = map[string]interface{}{}
	}
	meta := map[string]interface{}{"tags": tags}
	body := RequestExperimentCreate{
		Data: RequestData[RequestAttributesExperimentCreate]{
			Type: resourceTypeExperiments,
			Attributes: RequestAttributesExperimentCreate{
				ProjectID:      projectID,
				DatasetID:      datasetID,
				Name:           name,
				Description:    description,
				Metadata:       meta,
				Config:         expConfig,
				DatasetVersion: datasetVersion,
				EnsureUnique:   true,
			},
		},
	}

	status, b, err := c.request(ctx, method, path, body)
	if err != nil || status != http.StatusOK {
		return nil, fmt.Errorf("create experiment %q failed: %v (status=%d, body=%s)", name, err, status, string(b))
	}

	var resp ResponseExperimentCreate
	if err := json.Unmarshal(b, &resp); err != nil {
		return nil, fmt.Errorf("decode experiment response: %w", err)
	}
	exp := resp.Data.Attributes
	exp.ID = resp.Data.ID

	return &exp, nil
}

// ExperimentPushEvents -> POST /experiments/{id}/events  (accepts 200/202)
func (c *DNEClient) ExperimentPushEvents(
	ctx context.Context,
	experimentID string,
	metrics []ExperimentEvalMetricEvent,
	tags []string,
) error {
	path := fmt.Sprintf("%s/experiments/%s/events", basePathDNE, url.PathEscape(experimentID))
	method := http.MethodPost

	body := RequestExperimentPushEvents{
		Data: RequestData[RequestAttributesExperimentPushEvents]{
			Type: resourceTypeExperiments,
			Attributes: RequestAttributesExperimentPushEvents{
				Scope:   resourceTypeExperiments,
				Metrics: metrics,
				Tags:    tags,
			},
		},
	}

	status, b, err := c.request(ctx, method, path, body)
	if err != nil {
		return fmt.Errorf("post experiment eval metrics failed: %v (status=%d, body=%s)", err, status, string(b))
	}
	if status != http.StatusOK && status != http.StatusAccepted {
		return fmt.Errorf("unexpected status %d: %s", status, string(b))
	}
	return nil
}

// ---------- private stuff ----------

func (c *DNEClient) request(ctx context.Context, method, path string, body any) (int, []byte, error) {
	urlStr := c.baseURL + path

	var rdr io.Reader
	if body != nil {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(body); err != nil {
			return 0, nil, fmt.Errorf("encode body: %w", err)
		}
		rdr = &buf
	}

	req, err := http.NewRequestWithContext(ctx, method, urlStr, rdr)
	if err != nil {
		return 0, nil, err
	}

	for key, val := range c.defaultHeaders {
		req.Header.Set(key, val)
	}

	// TODO: review this makes sense
	backoffStrat := &backoff.ExponentialBackOff{
		InitialInterval:     defaultBackoff,
		RandomizationFactor: 0.5,
		Multiplier:          1.5,
		MaxInterval:         1 * time.Second,
	}

	doRequest := func() (*http.Response, error) {
		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()

		if resp.StatusCode < 200 || resp.StatusCode > 400 {
			return nil, fmt.Errorf("got a non-success error code: %d", resp.StatusCode)
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			// TODO: log.Debug something here
			rateLimitReset := resp.Header.Get(headerRateLimitReset)
			waitSeconds := 1
			if rateLimitReset != "" {
				if resetTime, err := strconv.ParseInt(rateLimitReset, 10, 64); err == nil {
					seconds := 0
					if resetTime > time.Now().Unix() {
						// Assume it's a Unix timestamp
						seconds = int(time.Until(time.Unix(resetTime, 0)).Seconds())
					} else {
						// Assume it's a duration in seconds
						seconds = int(resetTime)
					}
					if seconds > 0 {
						waitSeconds = seconds
					}
				}
			}
			return nil, backoff.RetryAfter(waitSeconds)
		}

		if resp.StatusCode >= 400 || resp.StatusCode <= 499 {
			return nil, backoff.Permanent(fmt.Errorf("client status error: %d", resp.StatusCode))
		}
		return resp, nil
	}

	resp, err := backoff.Retry(ctx, doRequest, backoff.WithBackOff(backoffStrat), backoff.WithMaxTries(defaultMaxRetries))
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, b, &httpError{Status: resp.StatusCode, Body: b}
	}
	return resp.StatusCode, b, nil
}

type httpError struct {
	Status int
	Body   []byte
}

func (e *httpError) Error() string {
	body := string(e.Body)
	if len(body) > 512 {
		body = body[:512] + "…"
	}
	return fmt.Sprintf("http %d: %s", e.Status, body)
}
