package dne

import (
	"errors"
	"reflect"

	"github.com/DataDog/dd-trace-go/v2/instrumentation/errortrace"
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
	Input          map[string]any `json:"input"`
	ExpectedOutput any            `json:"expected_output"`
	Metadata       map[string]any `json:"metadata"`
	Version        int            `json:"version"`
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
	Input          map[string]any `json:"input,omitempty"`
	ExpectedOutput any            `json:"expected_output,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
}

type DatasetRecordUpdate struct {
	ID             string         `json:"id"`
	Input          map[string]any `json:"input,omitempty"`
	ExpectedOutput *any           `json:"expected_output,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
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
	RequestDatasetCreate        = Request[DatasetCreate]
	RequestDatasetDelete        = Request[RequestAttributesDatasetDelete]
	RequestDatasetCreateRecords = Request[RequestAttributesDatasetCreateRecords]
	RequestDatasetBatchUpdate   = Request[RequestAttributesDatasetBatchUpdate]

	RequestProjectCreate = Request[RequestAttributesProjectCreate]

	RequestExperimentCreate     = Request[RequestAttributesExperimentCreate]
	RequestExperimentPushEvents = Request[RequestAttributesExperimentPushEvents]
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
	Type       string `json:"string"`
	Attributes T      `json:"attributes"`
}

type (
	ResponseDatasetGet           = ResponseList[DatasetView]
	ResponseDatasetCreate        = Response[DatasetView]
	ResponseDatasetUpdate        = Response[DatasetView]
	ResponseDatasetGetRecords    = ResponseList[DatasetRecordView]
	ResponseDatasetCreateRecords = ResponseList[DatasetRecordView]
	ResponseDatasetUpdateRecords = ResponseList[DatasetRecordView]
	ResponseDatasetBatchUpdate   = ResponseList[DatasetRecordView]

	ResponseProjectCreate = Response[ProjectView]

	ResponseExperimentCreate = Response[ExperimentView]
)

// ---------- Helpers ----------

// AnyPtr returns a pointer to the given value. This is used to create payloads that require pointers instead of values.
func AnyPtr[T any](v T) *T {
	return &v
}

// NewErrorMessage returns the payload representation of an error.
func NewErrorMessage(err error) *ErrorMessage {
	if err == nil {
		return nil
	}
	return &ErrorMessage{
		Message: err.Error(),
		Type:    errType(err),
		Stack:   errStackTrace(err),
	}
}

func errType(err error) string {
	var originalErr error
	var wErr *errortrace.TracerError
	if !errors.As(err, &wErr) {
		originalErr = err
	} else {
		originalErr = wErr.Unwrap()
	}
	return reflect.TypeOf(originalErr).String()
}

func errStackTrace(err error) string {
	var wErr *errortrace.TracerError
	if !errors.As(err, &wErr) {
		return ""
	}
	return wErr.Format()
}
