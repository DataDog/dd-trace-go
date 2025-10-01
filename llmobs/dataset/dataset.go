// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package dataset

import (
	"bufio"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"iter"
	"os"
	"slices"
	"sync"

	"github.com/google/uuid"

	illmobs "github.com/DataDog/dd-trace-go/v2/internal/llmobs"
	"github.com/DataDog/dd-trace-go/v2/internal/llmobs/transport"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

var (
	errRequiresAppKey      = errors.New(`an app key must be provided for the dataset configured via the DD_APP_KEY environment variable`)
	errRequiresProjectName = errors.New(`a project name must be provided for the dataset configured via the DD_LLM_OBS_ML_APP environment variable or tracer.WithLLMObsMLApp()`)
)

const experimentCSVFieldMaxSize = 10 * 1024 * 1024 // 10 MB

// Dataset represents a dataset for DataDog LLM Observability experiments.
type Dataset struct {
	mu sync.RWMutex

	name        string
	description string
	records     []*Record
	id          string
	version     int

	appendRecords map[string]*Record
	updateRecords map[string]*RecordUpdate
	deleteRecords map[string]struct{}
}

// Record represents a record in a Dataset.
type Record struct {
	Input          map[string]any
	ExpectedOutput any
	Metadata       map[string]any

	id      string
	version int
}

// ID returns the record id.
func (r *Record) ID() string {
	return r.id
}

// Version returns the record version.
func (r *Record) Version() int {
	return r.version
}

func (r *Record) applyUpdate(update RecordUpdate) {
	if update.Input != nil {
		r.Input = update.Input
	}
	if update.ExpectedOutput != nil {
		r.ExpectedOutput = update.ExpectedOutput
	}
	if update.Metadata != nil {
		r.Metadata = update.Metadata
	}
}

// RecordUpdate is used to represent partial record updates.
// Use nil to signal no modifications to a given field.
// Use empty values to signal deletion (e.g. empty strings or empty maps).
type RecordUpdate struct {
	Input          map[string]any
	ExpectedOutput any
	Metadata       map[string]any
}

func (u *RecordUpdate) merge(new RecordUpdate) {
	if new.Input != nil {
		u.Input = new.Input
	}
	if new.ExpectedOutput != nil {
		u.ExpectedOutput = new.ExpectedOutput
	}
	if new.Metadata != nil {
		u.Metadata = new.Metadata
	}
}

// Create initializes a Dataset and pushes it to DataDog.
func Create(ctx context.Context, name string, records []Record, opts ...CreateOption) (*Dataset, error) {
	ll, err := illmobs.ActiveLLMObs()
	if err != nil {
		return nil, err
	}
	cfg := defaultCreateConfig()
	for _, opt := range opts {
		opt(cfg)
	}

	// Validate required fields
	if ll.Config.TracerConfig.APPKey == "" {
		return nil, errRequiresAppKey
	}
	if ll.Config.ProjectName == "" {
		return nil, errRequiresProjectName
	}

	// Get or create project
	project, err := ll.Transport.GetOrCreateProject(ctx, ll.Config.ProjectName)
	if err != nil {
		return nil, fmt.Errorf("failed to get or create project: %w", err)
	}

	resp, err := ll.Transport.CreateDataset(ctx, name, cfg.description, project.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to create dataset: %w", err)
	}
	ds := &Dataset{
		id:          resp.ID,
		name:        resp.Name,
		description: resp.Description,
		version:     resp.CurrentVersion,
	}
	ds.Append(records...)
	if len(ds.records) > 0 {
		if err := ds.Push(ctx); err != nil {
			return nil, fmt.Errorf("failed to push dataset records: %w", err)
		}
	}
	return ds, nil
}

// CreateFromCSV creates a new dataset from a CSV file.
//
// Notes:
//   - CSV files must have a header row
//   - Maximum field size is 10MB
//   - All columns not specified in input_data_columns or expected_output_columns are automatically treated as metadata
//   - The dataset is automatically pushed to Datadog after creation
func CreateFromCSV(ctx context.Context, name, csvPath string, inputCols []string, opts ...CreateOption) (*Dataset, error) {
	ll, err := illmobs.ActiveLLMObs()
	if err != nil {
		return nil, err
	}
	cfg := defaultCreateConfig()
	for _, opt := range opts {
		opt(cfg)
	}

	// Validate required fields
	if ll.Config.TracerConfig.APPKey == "" {
		return nil, errRequiresAppKey
	}
	if ll.Config.ProjectName == "" {
		return nil, errRequiresProjectName
	}

	// Get or create project
	project, err := ll.Transport.GetOrCreateProject(ctx, ll.Config.ProjectName)
	if err != nil {
		return nil, fmt.Errorf("failed to get or create project: %w", err)
	}

	// 1) Create dataset
	resp, err := ll.Transport.CreateDataset(ctx, name, cfg.description, project.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to create dataset: %w", err)
	}
	ds := &Dataset{
		id:          resp.ID,
		name:        resp.Name,
		description: resp.Description,
		version:     resp.CurrentVersion,
	}

	// 2) Open CSV with a large buffer so big fields are supported
	f, err := os.Open(csvPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open csv: %w", err)
	}
	defer f.Close()

	br := bufio.NewReaderSize(f, experimentCSVFieldMaxSize+64*1024) // a bit over 10MB
	r := csv.NewReader(br)
	r.Comma = cfg.csvDelimiter
	r.FieldsPerRecord = -1 // allow variable columns; we enforce via header checks

	// 3) Read header
	header, err := r.Read()
	if err == io.EOF || (err == nil && len(header) == 0) {
		return nil, fmt.Errorf("CSV file appears to be empty or header is missing")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read header: %w", err)
	}

	headerIndex := make(map[string]int, len(header))
	for i, h := range header {
		headerIndex[h] = i
	}

	// 4) Validate required columns exist
	missing := func(required []string) []string {
		var m []string
		for _, col := range required {
			if _, ok := headerIndex[col]; !ok {
				m = append(m, col)
			}
		}
		return m
	}

	if bad := missing(inputCols); len(bad) > 0 {
		return nil, fmt.Errorf("input columns not found in CSV header: %v", bad)
	}
	if bad := missing(cfg.csvExpectedOutputCols); len(bad) > 0 {
		return nil, fmt.Errorf("expected output columns not found in CSV header: %v", bad)
	}
	if bad := missing(cfg.csvMetadataCols); len(bad) > 0 {
		return nil, fmt.Errorf("metadata columns not found in CSV header: %v", bad)
	}

	// 5) Stream rows and append
	for {
		record, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to read csv row: %w", err)
		}

		in := make(map[string]any, len(inputCols))
		for _, col := range inputCols {
			in[col] = record[headerIndex[col]]
		}

		out := make(map[string]any, len(cfg.csvExpectedOutputCols))
		for _, col := range cfg.csvExpectedOutputCols {
			out[col] = record[headerIndex[col]]
		}

		meta := make(map[string]any, len(cfg.csvMetadataCols))
		for _, col := range cfg.csvMetadataCols {
			meta[col] = record[headerIndex[col]]
		}

		ds.Append(Record{
			Input:          in,
			ExpectedOutput: out,
			Metadata:       meta,
		})
	}

	// 6) Push if any rows were added
	if len(ds.records) > 0 {
		if err := ds.Push(ctx); err != nil {
			return ds, fmt.Errorf("failed to push dataset records: %w", err)
		}
	}

	return ds, nil
}

// Pull fetches the given Dataset from DataDog.
func Pull(ctx context.Context, name string) (*Dataset, error) {
	ll, err := illmobs.ActiveLLMObs()
	if err != nil {
		return nil, err
	}

	// Validate required fields
	if ll.Config.ProjectName == "" {
		return nil, errRequiresProjectName
	}

	// Get or create project
	project, err := ll.Transport.GetOrCreateProject(ctx, ll.Config.ProjectName)
	if err != nil {
		return nil, fmt.Errorf("failed to get or create project: %w", err)
	}

	dsResp, recordsResp, err := ll.Transport.GetDatasetWithRecords(ctx, name, project.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to get dataset: %w", err)
	}

	records := make([]*Record, 0, len(recordsResp))
	for _, rec := range recordsResp {
		records = append(records, &Record{
			id:             rec.ID,
			Input:          rec.Input,
			ExpectedOutput: rec.ExpectedOutput,
			Metadata:       rec.Metadata,
			version:        rec.Version,
		})
	}
	ds := &Dataset{
		id:          dsResp.ID,
		name:        dsResp.Name,
		description: dsResp.Description,
		records:     records,
	}
	return ds, nil
}

// ID returns the dataset id.
func (d *Dataset) ID() string {
	return d.id
}

// Name returns the dataset name.
func (d *Dataset) Name() string {
	return d.name
}

// Version returns the dataset version.
func (d *Dataset) Version() int {
	return d.version
}

// Append adds new records to the Dataset.
func (d *Dataset) Append(records ...Record) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.initialize()

	for _, rec := range records {
		// This id will be discarded after push, since the backend will generate a new one.
		// It is used for tracking new records locally before the push.
		id := uuid.New().String()
		rec.id = id

		d.appendRecords[id] = &rec
		d.records = append(d.records, &rec)
	}
}

// Update updates the item at the given index.
func (d *Dataset) Update(index int, update RecordUpdate) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if index < 0 || index >= len(d.records) {
		log.Warn("llmobs: index %d out of range updating dataset record", index)
		return
	}
	if update.Input == nil && update.Metadata == nil && update.ExpectedOutput == nil {
		log.Warn("llmobs: invalid dataset update (no changes)")
		return
	}

	d.initialize()
	rec := d.records[index]
	if rec.id == "" {
		log.Warn("llmobs: invalid record with no ID at index %d, canceling update and removing record", index)
		d.records = slices.Delete(d.records, index, index+1)
		return
	}

	// if it is an addition that was not pushed yet, just modify the addition
	if _, ok := d.appendRecords[rec.id]; ok {
		rec.applyUpdate(update)
		return
	}

	// if there were updates before, just merge them
	if prevUpdate, ok := d.updateRecords[rec.id]; ok {
		prevUpdate.merge(update)
		return
	}
	d.updateRecords[rec.id] = &update
}

// Delete deletes the record at the given index.
func (d *Dataset) Delete(index int) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if index < 0 || index >= len(d.records) {
		log.Warn("llmobs: index %d out of range deleting dataset record", index)
		return
	}

	d.initialize()
	rec := d.records[index]
	if rec.id == "" {
		log.Warn("llmobs: invalid record with no ID at index %d, canceling deletion and removing record", index)
		d.records = slices.Delete(d.records, index, index+1)
		return
	}

	// additionally, remove it from append/update in case it was one
	delete(d.appendRecords, rec.id)
	delete(d.updateRecords, rec.id)
	d.deleteRecords[rec.id] = struct{}{}
	d.records = slices.Delete(d.records, index, index+1)
}

// Push pushes the Dataset changes to DataDog.
func (d *Dataset) Push(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.id == "" {
		return errors.New("dataset has no ID (create it using Create or CreateFromCSV)")
	}
	ll, err := illmobs.ActiveLLMObs()
	if err != nil {
		return err
	}
	d.initialize()

	insertOldIDs := make([]string, 0, len(d.appendRecords))
	insert := make([]transport.DatasetRecordCreate, 0, len(d.appendRecords))
	for id, rec := range d.appendRecords {
		insertOldIDs = append(insertOldIDs, id)
		insert = append(insert, transport.DatasetRecordCreate{
			Input:          rec.Input,
			ExpectedOutput: rec.ExpectedOutput,
			Metadata:       rec.Metadata,
		})
	}
	update := make([]transport.DatasetRecordUpdate, 0, len(d.updateRecords))
	for id, rec := range d.updateRecords {
		update = append(update, transport.DatasetRecordUpdate{
			ID:             id,
			Input:          rec.Input,
			ExpectedOutput: transport.AnyPtr(rec.ExpectedOutput),
			Metadata:       rec.Metadata,
		})
	}
	del := make([]string, 0, len(d.deleteRecords))
	for id := range d.deleteRecords {
		del = append(del, id)
	}

	// newRecordIDs should go in the same order
	newVersion, newRecordIDs, err := ll.Transport.BatchUpdateDataset(ctx, d.id, insert, update, del)
	if err != nil {
		return fmt.Errorf("failed to batch update dataset: %w", err)
	}

	// TODO(rarguelloF): migrate to new backend response format so this is not necessary
	if len(insertOldIDs) != len(newRecordIDs) {
		return fmt.Errorf("received a different number of new records than what it was sent (want: %d, got :%d)", len(insertOldIDs), len(newRecordIDs))
	}

	// FIXME(rarguelloF): we don't get version numbers in responses to deletion requests
	if newVersion > 0 {
		d.version = newVersion
	} else {
		d.version++
	}

	// update the inserted records with the new IDs generated by the backend
	for i, newID := range newRecordIDs {
		oldID := insertOldIDs[i]
		d.appendRecords[oldID].id = newID
	}
	d.appendRecords = make(map[string]*Record)
	d.updateRecords = make(map[string]*RecordUpdate)
	d.deleteRecords = make(map[string]struct{})

	return nil
}

// URL returns the url to access the dataset in DataDog.
func (d *Dataset) URL() string {
	// FIXME(rarguelloF): will not work for subdomain orgs
	return fmt.Sprintf("%s/llm/datasets/%s", illmobs.PublicResourceBaseURL(), d.id)
}

// Len returns the length of the dataset records.
func (d *Dataset) Len() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return len(d.records)
}

// Records returns an iterator with copies of the records in the Dataset.
// To modify the records, use the Append, Update or Delete methods.
//
// Warning: Do not call any of the methods that modify the records while in the loop, or you will cause a deadlock.
func (d *Dataset) Records() iter.Seq2[int, Record] {
	return func(yield func(int, Record) bool) {
		d.mu.RLock()
		defer d.mu.RUnlock()

		for i, rec := range d.records {
			if !yield(i, *rec) {
				return
			}
		}
	}
}

// Record returns the record at the given index.
func (d *Dataset) Record(idx int) (Record, bool) {
	if idx < 0 || idx >= len(d.records) {
		return Record{}, false
	}
	rec := d.records[idx]
	return *rec, true
}

func (d *Dataset) initialize() {
	if d.records == nil {
		d.records = make([]*Record, 0)
	}
	if d.appendRecords == nil {
		d.appendRecords = make(map[string]*Record)
	}
	if d.updateRecords == nil {
		d.updateRecords = make(map[string]*RecordUpdate)
	}
	if d.deleteRecords == nil {
		d.deleteRecords = make(map[string]struct{})
	}
}
