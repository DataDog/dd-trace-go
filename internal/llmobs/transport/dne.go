// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

const (
	resourceTypeDatasets    = "datasets"
	resourceTypeExperiments = "experiments"
	resourceTypeProjects    = "projects"
)

// ---------- Resources ----------

type DatasetView struct {
	ID             string
	Name           string         `json:"name"`
	Description    string         `json:"description"`
	Metadata       map[string]any `json:"metadata"`
	CurrentVersion int            `json:"current_version"`
}

type DatasetCreate struct {
	Name        string         `json:"name,omitempty"`
	Description string         `json:"description,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

type DatasetRecordView struct {
	ID             string
	Input          any `json:"input"`
	ExpectedOutput any `json:"expected_output"`
	Metadata       any `json:"metadata"`
	Version        int `json:"version"`
}

type ProjectView struct {
	ID   string
	Name string `json:"name"`
}

type ExperimentView struct {
	ID             string
	ProjectID      string         `json:"project_id"`
	DatasetID      string         `json:"dataset_id"`
	Name           string         `json:"name"`
	Description    string         `json:"description"`
	Metadata       map[string]any `json:"metadata"`
	Config         map[string]any `json:"config"`
	DatasetVersion int            `json:"dataset_version"`
	EnsureUnique   bool           `json:"ensure_unique"`
}

type DatasetRecordCreate struct {
	Input          any `json:"input,omitempty"`
	ExpectedOutput any `json:"expected_output,omitempty"`
	Metadata       any `json:"metadata,omitempty"`
}

type DatasetRecordUpdate struct {
	ID             string `json:"id"`
	Input          any    `json:"input,omitempty"`
	ExpectedOutput *any   `json:"expected_output,omitempty"`
	Metadata       any    `json:"metadata,omitempty"`
}

type ErrorMessage struct {
	Message string `json:"message,omitempty"`
	Type    string `json:"type,omitempty"`
	Stack   string `json:"stack,omitempty"`
}

// ---------- Requests ----------

type Request[T any] struct {
	Data RequestData[T] `json:"data"`
}

type RequestData[T any] struct {
	Type       string `json:"type"`
	Attributes T      `json:"attributes"`
}

type RequestAttributesDatasetCreateRecords struct {
	Records []DatasetRecordCreate `json:"records,omitempty"`
}

type RequestAttributesDatasetDelete struct {
	DatasetIDs []string `json:"dataset_ids,omitempty"`
}

type RequestAttributesDatasetBatchUpdate struct {
	InsertRecords []DatasetRecordCreate `json:"insert_records,omitempty"`
	UpdateRecords []DatasetRecordUpdate `json:"update_records,omitempty"`
	DeleteRecords []string              `json:"delete_records,omitempty"`
	Deduplicate   *bool                 `json:"deduplicate,omitempty"`
}

type RequestAttributesProjectCreate struct {
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
}

type RequestAttributesExperimentCreate struct {
	ProjectID      string         `json:"project_id,omitempty"`
	DatasetID      string         `json:"dataset_id,omitempty"`
	Name           string         `json:"name,omitempty"`
	Description    string         `json:"description,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
	Config         map[string]any `json:"config,omitempty"`
	DatasetVersion int            `json:"dataset_version,omitempty"`
	EnsureUnique   bool           `json:"ensure_unique,omitempty"`
}

type RequestAttributesExperimentPushEvents struct {
	Scope   string                      `json:"scope,omitempty"`
	Metrics []ExperimentEvalMetricEvent `json:"metrics,omitempty"`
	Tags    []string                    `json:"tags,omitempty"`
}

type ExperimentEvalMetricEvent struct {
	SpanID           string        `json:"span_id,omitempty"`
	TraceID          string        `json:"trace_id,omitempty"`
	TimestampMS      int64         `json:"timestamp_ms,omitempty"`
	MetricType       string        `json:"metric_type,omitempty"`
	Label            string        `json:"label,omitempty"`
	CategoricalValue *string       `json:"categorical_value,omitempty"`
	ScoreValue       *float64      `json:"score_value,omitempty"`
	BooleanValue     *bool         `json:"boolean_value,omitempty"`
	Error            *ErrorMessage `json:"error,omitempty"`
	Tags             []string      `json:"tags,omitempty"`
	ExperimentID     string        `json:"experiment_id,omitempty"`
}

type (
	CreateDatasetRequest        = Request[DatasetCreate]
	DeleteDatasetRequest        = Request[RequestAttributesDatasetDelete]
	CreateDatasetRecordsRequest = Request[RequestAttributesDatasetCreateRecords]
	BatchUpdateDatasetRequest   = Request[RequestAttributesDatasetBatchUpdate]

	CreateProjectRequest = Request[RequestAttributesProjectCreate]

	CreateExperimentRequest     = Request[RequestAttributesExperimentCreate]
	PushExperimentEventsRequest = Request[RequestAttributesExperimentPushEvents]
)

// ---------- Responses ----------

type Response[T any] struct {
	Data ResponseData[T] `json:"data"`
}

type ResponseList[T any] struct {
	Data []ResponseData[T] `json:"data"`
}

type ResponseData[T any] struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	Attributes T      `json:"attributes"`
}

type (
	GetDatasetResponse    = ResponseList[DatasetView]
	CreateDatasetResponse = Response[DatasetView]
	UpdateDatasetResponse = Response[DatasetView]

	GetDatasetRecordsResponse    = ResponseList[DatasetRecordView]
	CreateDatasetRecordsResponse = ResponseList[DatasetRecordView]
	UpdateDatasetRecordsResponse = ResponseList[DatasetRecordView]
	BatchUpdateDatasetResponse   = ResponseList[DatasetRecordView]

	CreateProjectResponse = Response[ProjectView]

	CreateExperimentResponse = Response[ExperimentView]
)

func (c *Transport) GetDatasetByName(ctx context.Context, name, projectID string) (*DatasetView, error) {
	q := url.Values{}
	q.Set("filter[name]", name)
	datasetPath := fmt.Sprintf("%s/%s/datasets?%s", endpointPrefixDNE, url.PathEscape(projectID), q.Encode())
	method := http.MethodGet

	status, b, err := c.request(ctx, method, datasetPath, subdomainDNE, nil)
	if err != nil || status != http.StatusOK {
		return nil, fmt.Errorf("get dataset by name %q failed: %v", name, err)
	}

	var datasetResp GetDatasetResponse
	if err := json.Unmarshal(b, &datasetResp); err != nil {
		return nil, fmt.Errorf("decode datasets list: %w", err)
	}
	if len(datasetResp.Data) == 0 {
		return nil, ErrDatasetNotFound
	}
	ds := datasetResp.Data[0].Attributes
	ds.ID = datasetResp.Data[0].ID
	return &ds, nil
}

func (c *Transport) CreateDataset(ctx context.Context, name, description, projectID string) (*DatasetView, error) {
	_, err := c.GetDatasetByName(ctx, name, projectID)
	if err == nil {
		return nil, errors.New("dataset already exists")
	}
	if !errors.Is(err, ErrDatasetNotFound) {
		return nil, err
	}

	path := fmt.Sprintf("%s/%s/datasets", endpointPrefixDNE, url.PathEscape(projectID))
	method := http.MethodPost
	body := CreateDatasetRequest{
		Data: RequestData[DatasetCreate]{
			Type: resourceTypeDatasets,
			Attributes: DatasetCreate{
				Name:        name,
				Description: description,
			},
		},
	}

	status, b, err := c.request(ctx, method, path, subdomainDNE, body)
	if err != nil {
		return nil, fmt.Errorf("create dataset %q failed: %v", name, err)
	}

	log.Debug("llmobs/internal/transport.DatasetGetOrCreate: create dataset success (status code: %d)", status)

	var resp CreateDatasetResponse
	if err := json.Unmarshal(b, &resp); err != nil {
		return nil, fmt.Errorf("decode create dataset response: %w", err)
	}
	id := resp.Data.ID
	dataset := resp.Data.Attributes
	dataset.ID = id
	return &dataset, nil
}

func (c *Transport) DeleteDataset(ctx context.Context, datasetIDs ...string) error {
	path := endpointPrefixDNE + "/datasets/delete"
	method := http.MethodPost
	body := DeleteDatasetRequest{
		Data: RequestData[RequestAttributesDatasetDelete]{
			Type: resourceTypeDatasets,
			Attributes: RequestAttributesDatasetDelete{
				DatasetIDs: datasetIDs,
			},
		},
	}

	status, _, err := c.request(ctx, method, path, subdomainDNE, body)
	if err != nil || status != http.StatusOK {
		return fmt.Errorf("delete dataset %v failed: %v", datasetIDs, err)
	}
	return nil
}

func (c *Transport) BatchUpdateDataset(
	ctx context.Context,
	datasetID string,
	insert []DatasetRecordCreate,
	update []DatasetRecordUpdate,
	delete []string,
) (int, []string, error) {
	path := fmt.Sprintf("%s/datasets/%s/batch_update", endpointPrefixDNE, url.PathEscape(datasetID))
	method := http.MethodPost
	body := BatchUpdateDatasetRequest{
		Data: RequestData[RequestAttributesDatasetBatchUpdate]{
			Type: resourceTypeDatasets,
			Attributes: RequestAttributesDatasetBatchUpdate{
				InsertRecords: insert,
				UpdateRecords: update,
				DeleteRecords: delete,
				Deduplicate:   AnyPtr(false),
			},
		},
	}

	status, b, err := c.request(ctx, method, path, subdomainDNE, body)
	if err != nil || status != http.StatusOK {
		return -1, nil, fmt.Errorf("batch_update for dataset %q failed: %v", datasetID, err)
	}

	var resp BatchUpdateDatasetResponse
	if err := json.Unmarshal(b, &resp); err != nil {
		return -1, nil, fmt.Errorf("decode batch_update response: %w", err)
	}

	// FIXME: we don't get version numbers in responses to deletion requests
	// TODO(rarguelloF): the backend could return a better response here...
	var (
		newDatasetVersion = -1
		newRecordIDs      []string
	)
	if len(resp.Data) > 0 {
		if resp.Data[0].Attributes.Version > 0 {
			newDatasetVersion = resp.Data[0].Attributes.Version
		}
	}
	if len(resp.Data) == len(insert)+len(update) {
		// new records are at the end of the slice
		for _, rec := range resp.Data[len(update):] {
			newRecordIDs = append(newRecordIDs, rec.ID)
		}
	} else {
		log.Warn("llmobs/internal/transport: BatchUpdateDataset: expected %d records in response, got %d", len(insert)+len(update), len(resp.Data))
	}
	return newDatasetVersion, newRecordIDs, nil
}

func (c *Transport) GetDatasetWithRecords(ctx context.Context, name, projectID string) (*DatasetView, []DatasetRecordView, error) {
	// 1) Fetch record by name
	ds, err := c.GetDatasetByName(ctx, name, projectID)
	if err != nil {
		return nil, nil, err
	}

	// 2) Fetch records
	method := http.MethodGet
	recordsPath := fmt.Sprintf("%s/datasets/%s/records", endpointPrefixDNE, url.PathEscape(ds.ID))
	status, b, err := c.request(ctx, method, recordsPath, subdomainDNE, nil)
	if err != nil || status != http.StatusOK {
		return nil, nil, fmt.Errorf("get dataset records failed: %v (name=%q, status=%d)", err, name, status)
	}

	var recordsResp GetDatasetRecordsResponse
	if err := json.Unmarshal(b, &recordsResp); err != nil {
		return nil, nil, fmt.Errorf("decode dataset records: %w", err)
	}

	records := make([]DatasetRecordView, 0, len(recordsResp.Data))
	for _, r := range recordsResp.Data {
		rec := r.Attributes
		rec.ID = r.ID
		records = append(records, rec)
	}
	return ds, records, nil
}

func (c *Transport) GetOrCreateProject(ctx context.Context, name string) (*ProjectView, error) {
	path := endpointPrefixDNE + "/projects"
	method := http.MethodPost

	body := CreateProjectRequest{
		Data: RequestData[RequestAttributesProjectCreate]{
			Type: resourceTypeProjects,
			Attributes: RequestAttributesProjectCreate{
				Name:        name,
				Description: "",
			},
		},
	}
	status, b, err := c.request(ctx, method, path, subdomainDNE, body)
	if err != nil || status != http.StatusOK {
		return nil, fmt.Errorf("create project %q failed: %v", name, err)
	}

	var resp CreateProjectResponse
	if err := json.Unmarshal(b, &resp); err != nil {
		return nil, fmt.Errorf("decode project response: %w", err)
	}

	project := resp.Data.Attributes
	project.ID = resp.Data.ID
	return &project, nil
}

func (c *Transport) CreateExperiment(
	ctx context.Context,
	name, datasetID, projectID string,
	datasetVersion int,
	expConfig map[string]any,
	tags []string,
	description string,
) (*ExperimentView, error) {
	path := endpointPrefixDNE + "/experiments"
	method := http.MethodPost

	if expConfig == nil {
		expConfig = map[string]interface{}{}
	}
	meta := map[string]interface{}{"tags": tags}
	body := CreateExperimentRequest{
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

	status, b, err := c.request(ctx, method, path, subdomainDNE, body)
	if err != nil || status != http.StatusOK {
		return nil, fmt.Errorf("create experiment %q failed: %v", name, err)
	}

	var resp CreateExperimentResponse
	if err := json.Unmarshal(b, &resp); err != nil {
		return nil, fmt.Errorf("decode experiment response: %w", err)
	}
	exp := resp.Data.Attributes
	exp.ID = resp.Data.ID

	return &exp, nil
}

func (c *Transport) PushExperimentEvents(
	ctx context.Context,
	experimentID string,
	metrics []ExperimentEvalMetricEvent,
	tags []string,
) error {
	path := fmt.Sprintf("%s/experiments/%s/events", endpointPrefixDNE, url.PathEscape(experimentID))
	method := http.MethodPost

	body := PushExperimentEventsRequest{
		Data: RequestData[RequestAttributesExperimentPushEvents]{
			Type: resourceTypeExperiments,
			Attributes: RequestAttributesExperimentPushEvents{
				Scope:   resourceTypeExperiments,
				Metrics: metrics,
				Tags:    tags,
			},
		},
	}

	status, b, err := c.request(ctx, method, path, subdomainDNE, body)
	if err != nil {
		return fmt.Errorf("post experiment eval metrics failed: %v", err)
	}
	if status != http.StatusOK && status != http.StatusAccepted {
		return fmt.Errorf("unexpected status %d: %s", status, string(b))
	}
	return nil
}
