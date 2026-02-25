// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package devserver

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"

	"github.com/DataDog/dd-trace-go/v2/llmobs/dataset"
	"github.com/DataDog/dd-trace-go/v2/llmobs/experiment"
)

// EvalRequest is the JSON body for POST /eval.
type EvalRequest struct {
	Name           string         `json:"name"`
	Stream         bool           `json:"stream"`
	ConfigOverride map[string]any `json:"config_override,omitempty"`
	Evaluators     []string       `json:"evaluators,omitempty"`
	SampleSize     int            `json:"sample_size,omitempty"`
	DatasetName    string         `json:"dataset_name,omitempty"`
}

// listExperimentView is the JSON representation of a single experiment in /list.
type listExperimentView struct {
	Name        string                  `json:"name"`
	Description string                  `json:"description"`
	ProjectName string                  `json:"project_name"`
	TaskName    string                  `json:"task_name"`
	DatasetName string                  `json:"dataset_name"`
	DatasetLen  int                     `json:"dataset_len"`
	Evaluators  []string                `json:"evaluators"`
	Config      map[string]*ConfigField `json:"config"`
	Tags        map[string]string       `json:"tags,omitempty"`
}

// NewListHandler returns an http.Handler for GET /list.
// It reads the registry and returns JSON describing all registered experiments.
func NewListHandler(registry *Registry) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeErrorJSON(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		exps := registry.List()
		views := make([]listExperimentView, 0, len(exps))
		for _, def := range exps {
			evNames := make([]string, 0, len(def.Evaluators))
			for _, ev := range def.Evaluators {
				evNames = append(evNames, ev.Name())
			}
			views = append(views, listExperimentView{
				Name:        def.Name,
				Description: def.Description,
				ProjectName: def.ProjectName,
				TaskName:    def.Task.Name(),
				DatasetName: def.Dataset.Name(),
				DatasetLen:  def.Dataset.Len(),
				Evaluators:  evNames,
				Config:      def.Config,
				Tags:        def.Tags,
			})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"experiments": views})
	})
}

// NewEvalHandler returns an http.Handler for POST /eval.
// It looks up the experiment by name, merges config overrides, filters evaluators,
// and runs the experiment. In streaming mode, it writes newline-delimited JSON events.
func NewEvalHandler(registry *Registry) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErrorJSON(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}

		var req EvalRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErrorJSON(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
			return
		}
		if req.Name == "" {
			writeErrorJSON(w, http.StatusBadRequest, "experiment name is required")
			return
		}

		def, ok := registry.Get(req.Name)
		if !ok {
			writeErrorJSON(w, http.StatusNotFound, "experiment not found: "+req.Name)
			return
		}

		// Resolve dataset: use override if provided, otherwise use the registered default.
		ds := def.Dataset
		if req.DatasetName != "" && req.DatasetName != def.Dataset.Name() {
			var pullOpts []dataset.PullOption
			if def.ProjectName != "" {
				pullOpts = append(pullOpts, dataset.WithPullProjectName(def.ProjectName))
			}
			pulled, err := dataset.Pull(r.Context(), req.DatasetName, pullOpts...)
			if err != nil {
				writeErrorJSON(w, http.StatusBadRequest, "failed to pull dataset: "+err.Error())
				return
			}
			ds = pulled
		}

		mergedCfg := mergeConfig(defaultsFromConfig(def.Config), req.ConfigOverride)
		evaluators := filterEvaluators(def.Evaluators, req.Evaluators)

		var opts []experiment.Option
		if def.ProjectName != "" {
			opts = append(opts, experiment.WithProjectName(def.ProjectName))
		}
		if def.Description != "" {
			opts = append(opts, experiment.WithDescription(def.Description))
		}
		if mergedCfg != nil {
			opts = append(opts, experiment.WithExperimentConfig(mergedCfg))
		}
		if len(def.Tags) > 0 {
			opts = append(opts, experiment.WithTags(def.Tags))
		}
		if len(def.SummaryEvaluators) > 0 {
			opts = append(opts, experiment.WithSummaryEvaluators(def.SummaryEvaluators...))
		}

		exp, err := experiment.New(def.Name, def.Task, ds, evaluators, opts...)
		if err != nil {
			writeErrorJSON(w, http.StatusInternalServerError, "failed to create experiment: "+err.Error())
			return
		}

		var runOpts []experiment.RunOption
		if req.SampleSize > 0 {
			runOpts = append(runOpts, experiment.WithSampleSize(req.SampleSize))
		}

		if req.Stream {
			handleStreamingEval(w, r, exp, def, ds, runOpts)
		} else {
			handleSyncEval(w, r, exp, runOpts)
		}
	})
}

func handleStreamingEval(w http.ResponseWriter, r *http.Request, exp *experiment.Experiment, def *ExperimentDefinition, ds *dataset.Dataset, runOpts []experiment.RunOption) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErrorJSON(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	var mu sync.Mutex
	enc := json.NewEncoder(w)

	writeEvent := func(event StreamEvent) {
		mu.Lock()
		defer mu.Unlock()
		enc.Encode(event)
		flusher.Flush()
	}

	runOpts = append(runOpts,
		experiment.WithOnExperimentCreated(func(id, name string) {
			writeEvent(StreamEvent{
				Event: "start",
				Data: StartEventData{
					ExperimentName: name,
					ProjectName:    def.ProjectName,
					ExperimentID:   id,
					DatasetName:    ds.Name(),
					TotalRows:      ds.Len(),
				},
			})
		}),
		experiment.WithProgressCallback(func(pe experiment.ProgressEvent) {
			pd := ProgressEventData{
				RowIndex: pe.RecordIndex,
				Status:   string(pe.Status),
			}
			switch pe.Status {
			case experiment.ProgressRunning:
				if pe.Record != nil {
					pd.Input = pe.Record.Input
					pd.ExpectedOutput = pe.Record.ExpectedOutput
				}
			case experiment.ProgressTaskComplete:
				pd.Output = pe.Output
				pd.Span = pe.SpanEvent
			case experiment.ProgressEvaluationsComplete:
				pd.Span = pe.SpanEvent
				pd.EvalMetrics = pe.EvalMetrics
				if pe.Evaluations != nil {
					evals := make(map[string]any, len(pe.Evaluations))
					for _, ev := range pe.Evaluations {
						if ev.Error != nil {
							evals[ev.Name] = map[string]any{"error": ev.Error.Error()}
						} else {
							evals[ev.Name] = ev.Value
						}
					}
					pd.Evaluations = evals
				}
			case experiment.ProgressError:
				pd.Span = pe.SpanEvent
				if pe.Error != nil {
					pd.Error = &ErrorData{Message: pe.Error.Error()}
				}
			case experiment.ProgressSuccess:
				pd.Span = pe.SpanEvent
				pd.EvalMetrics = pe.EvalMetrics
			}
			writeEvent(StreamEvent{Event: "progress", Data: pd})
		}),
	)

	result, err := exp.Run(r.Context(), runOpts...)
	if err != nil {
		writeEvent(StreamEvent{
			Event: "error",
			Data:  ErrorData{Message: err.Error()},
		})
		return
	}

	scores := buildSummaryScores(result)
	writeEvent(StreamEvent{
		Event: "summary",
		Data: SummaryEventData{
			Scores:  scores,
			Metrics: make(map[string]any),
		},
	})
	writeEvent(StreamEvent{Event: "done", Data: nil})
}

func handleSyncEval(w http.ResponseWriter, r *http.Request, exp *experiment.Experiment, runOpts []experiment.RunOption) {
	result, err := exp.Run(r.Context(), runOpts...)
	if err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, "experiment run failed: "+err.Error())
		return
	}

	scores := buildSummaryScores(result)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"experiment_name": result.ExperimentName,
		"dataset_name":    result.DatasetName,
		"results":         result.Results,
		"scores":          scores,
	})
}

func buildSummaryScores(result *experiment.ExperimentResult) map[string]any {
	scores := make(map[string]any)
	if result == nil || len(result.Results) == 0 {
		return scores
	}
	// Aggregate evaluation scores across all records
	evalSums := make(map[string]float64)
	evalCounts := make(map[string]int)
	for _, res := range result.Results {
		for _, ev := range res.Evaluations {
			if ev.Error != nil {
				continue
			}
			val, ok := toFloat64(ev.Value)
			if !ok {
				continue
			}
			evalSums[ev.Name] += val
			evalCounts[ev.Name]++
		}
	}
	for name, sum := range evalSums {
		scores[name] = sum / float64(evalCounts[name])
	}
	// Include summary evaluations
	for _, ev := range result.SummaryEvaluations {
		if ev.Error == nil {
			scores[ev.Name] = ev.Value
		}
	}
	return scores
}

// toFloat64 converts a numeric or boolean value to float64 for score aggregation.
// Returns (value, true) for supported types, or (0, false) for unsupported types like strings.
func toFloat64(v any) (float64, bool) {
	switch v := v.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int8:
		return float64(v), true
	case int16:
		return float64(v), true
	case int32:
		return float64(v), true
	case int64:
		return float64(v), true
	case uint:
		return float64(v), true
	case uint8:
		return float64(v), true
	case uint16:
		return float64(v), true
	case uint32:
		return float64(v), true
	case uint64:
		return float64(v), true
	case bool:
		if v {
			return 1, true
		}
		return 0, true
	default:
		return 0, false
	}
}

// corsMiddleware wraps a handler with CORS support.
func corsMiddleware(next http.Handler, origins []string) http.Handler {
	originStr := strings.Join(origins, ", ")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", originStr)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeErrorJSON(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}
