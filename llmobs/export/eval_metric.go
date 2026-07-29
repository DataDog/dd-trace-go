// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package export

import (
	"context"
	"fmt"

	illmobs "github.com/DataDog/dd-trace-go/v2/internal/llmobs"
	"github.com/DataDog/dd-trace-go/v2/internal/llmobs/transport"
	"github.com/DataDog/dd-trace-go/v2/llmobs"
)

// MetricType is the type of an evaluation metric value.
type MetricType = llmobs.EvalMetricType

const (
	MetricTypeCategorical MetricType = llmobs.EvalMetricTypeCategorical
	MetricTypeScore       MetricType = llmobs.EvalMetricTypeScore
	MetricTypeBoolean     MetricType = llmobs.EvalMetricTypeBoolean
	MetricTypeJSON        MetricType = llmobs.EvalMetricTypeJSON
)

// EvaluationMetric is a caller-built LLM Obs evaluation metric.
type EvaluationMetric = illmobs.EvaluationConfig

// SubmitEvaluations submits LLM Obs evaluation metrics.
func (c *Client) SubmitEvaluations(ctx context.Context, evals []EvaluationMetric, opts ...SubmitEvaluationsOption) (*Result, error) {
	sc := c.resolveSubmitEvaluations(opts)
	res := &Result{}
	size := defaultEvalBatchSize
	for start := 0; start < len(evals); start += size {
		if err := ctx.Err(); err != nil {
			return res, res.recordCancel(inputIndices(start, len(evals)), err)
		}
		end := min(start+size, len(evals))
		batch := make([]evalRow, 0, end-start)
		for i := start; i < end; i++ {
			metric, validation := illmobs.BuildExportEvaluation(evals[i], sc.mlApp)
			if validation != nil {
				validation.Index = i
				res.ValidationErrors = append(res.ValidationErrors, *validation)
				continue
			}
			batch = append(batch, evalRow{index: i, metric: metric})
		}
		c.sendEvalBatch(ctx, batch, res)
	}
	res.finalize()
	if err := res.canceledErr(); err != nil {
		return res, err
	}
	return res, aggregateFailures(res)
}

type evalRow struct {
	index  int
	metric *transport.LLMObsMetric
}

func (c *Client) sendEvalBatch(ctx context.Context, batch []evalRow, res *Result) {
	if len(batch) == 0 {
		return
	}
	metrics := make([]*transport.LLMObsMetric, len(batch))
	for i := range batch {
		metrics[i] = batch[i].metric
	}
	payload := transport.NewPushMetricsRequest(metrics)
	body, err := transport.MarshalJSON(payload)
	if err != nil {
		good := dropUnencodableEvals(batch, res)
		if len(good) == len(batch) {
			res.Requests = append(res.Requests, RequestResult{
				InputIndices: evalInputIndices(batch),
				Err:          fmt.Errorf("llmobs/export: encode eval payload: %w", err),
			})
			return
		}
		if len(good) > 0 {
			c.sendEvalBatch(ctx, good, res)
		}
		return
	}
	if len(body) > illmobs.SizeLimitEVPEvent {
		if len(batch) == 1 {
			res.ValidationErrors = append(res.ValidationErrors, ValidationError{
				Index:  batch[0].index,
				Code:   CodeTooLarge,
				Reason: "evaluation exceeds the LLM Obs event size limit",
			})
			return
		}
		mid := len(batch) / 2
		c.sendEvalBatch(ctx, batch[:mid], res)
		if err := ctx.Err(); err != nil {
			_ = res.recordCancel(evalInputIndices(batch[mid:]), err)
			return
		}
		c.sendEvalBatch(ctx, batch[mid:], res)
		return
	}
	rr := RequestResult{InputIndices: evalInputIndices(batch)}
	result, requestErr := c.transport.PushEvalMetricsWithResult(ctx, metrics)
	applyResult(&rr, result, requestErr)
	res.Requests = append(res.Requests, rr)
}

func dropUnencodableEvals(batch []evalRow, res *Result) []evalRow {
	good := make([]evalRow, 0, len(batch))
	for _, row := range batch {
		if _, err := transport.MarshalJSON(row.metric); err != nil {
			res.ValidationErrors = append(res.ValidationErrors, ValidationError{
				Index:  row.index,
				Code:   CodeNotEncodable,
				Reason: "evaluation is not JSON-encodable: " + err.Error(),
			})
			continue
		}
		good = append(good, row)
	}
	return good
}

func evalInputIndices(batch []evalRow) []int {
	indices := make([]int, len(batch))
	for i, row := range batch {
		indices[i] = row.index
	}
	return indices
}
