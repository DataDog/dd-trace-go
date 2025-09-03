/*
 * Unless explicitly stated otherwise all files in this repository are licensed
 * under the Apache License Version 2.0.
 *
 * This product includes software developed at Datadog (https://www.datadoghq.com/).
 * Copyright 2021 Datadog, Inc.
 */

package metrics

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type (
	mockClient struct {
		batches                chan []APIMetric
		sendMetricsCalledCount int
		err                    error
	}

	mockTimeService struct {
		now        time.Time
		tickerChan chan time.Time
	}
)

func makeMockClient() mockClient {
	return mockClient{
		batches: make(chan []APIMetric, 10),
		err:     nil,
	}
}

func makeMockTimeService() mockTimeService {
	return mockTimeService{
		now:        time.Now(),
		tickerChan: make(chan time.Time),
	}
}

func (mc *mockClient) SendMetrics(mts []APIMetric) error {
	mc.sendMetricsCalledCount++
	mc.batches <- mts
	return mc.err
}

func (ts *mockTimeService) NewTicker(duration time.Duration) *time.Ticker {
	return &time.Ticker{
		C: ts.tickerChan,
	}
}

func (ts *mockTimeService) Now() time.Time {
	return ts.now
}

func TestProcessorBatches(t *testing.T) {
	mc := makeMockClient()
	mts := makeMockTimeService()

	mts.now, _ = time.Parse(time.RFC3339, "2006-01-02T15:04:05Z")
	nowUnix := float64(mts.now.Unix())

	processor := MakeProcessor(context.Background(), &mc, &mts, 1000, false, time.Hour*1000, time.Hour*1000, math.MaxUint32)

	d1 := Distribution{
		Name:   "metric-1",
		Tags:   []string{"a", "b", "c"},
		Values: []MetricValue{{Timestamp: mts.now, Value: 1}, {Timestamp: mts.now, Value: 2}, {Timestamp: mts.now, Value: 3}},
	}
	d2 := Distribution{
		Name:   "metric-1",
		Tags:   []string{"a", "b", "c"},
		Values: []MetricValue{{Timestamp: mts.now, Value: 4}, {Timestamp: mts.now, Value: 5}, {Timestamp: mts.now, Value: 6}},
	}

	processor.AddMetric(&d1)
	processor.AddMetric(&d2)

	processor.StartProcessing()
	processor.FinishProcessing()

	firstBatch := <-mc.batches

	assert.Equal(t, []APIMetric{{
		Name:       "metric-1",
		Tags:       []string{"a", "b", "c"},
		MetricType: DistributionType,
		Points: []interface{}{
			[]interface{}{nowUnix, []interface{}{float64(1)}},
			[]interface{}{nowUnix, []interface{}{float64(2)}},
			[]interface{}{nowUnix, []interface{}{float64(3)}},
			[]interface{}{nowUnix, []interface{}{float64(4)}},
			[]interface{}{nowUnix, []interface{}{float64(5)}},
			[]interface{}{nowUnix, []interface{}{float64(6)}},
		},
	}}, firstBatch)
}

func TestProcessorBatchesPerTick(t *testing.T) {
	mc := makeMockClient()
	mts := makeMockTimeService()

	firstTime, _ := time.Parse(time.RFC3339, "2006-01-02T15:04:05Z")
	firstTimeUnix := float64(firstTime.Unix())
	secondTime, _ := time.Parse(time.RFC3339, "2007-01-02T15:04:05Z")
	secondTimeUnix := float64(secondTime.Unix())
	mts.now = firstTime

	processor := MakeProcessor(context.Background(), &mc, &mts, 1000, false, time.Hour*1000, time.Hour*1000, math.MaxUint32)

	d1 := Distribution{
		Name:   "metric-1",
		Tags:   []string{"a", "b", "c"},
		Values: []MetricValue{{Timestamp: firstTime, Value: 1}, {Timestamp: firstTime, Value: 2}},
	}
	d2 := Distribution{
		Name:   "metric-1",
		Tags:   []string{"a", "b", "c"},
		Values: []MetricValue{{Timestamp: firstTime, Value: 3}},
	}
	d3 := Distribution{
		Name:   "metric-1",
		Tags:   []string{"a", "b", "c"},
		Values: []MetricValue{{Timestamp: secondTime, Value: 4}, {Timestamp: secondTime, Value: 5}},
	}
	d4 := Distribution{
		Name:   "metric-1",
		Tags:   []string{"a", "b", "c"},
		Values: []MetricValue{{Timestamp: secondTime, Value: 6}},
	}

	processor.StartProcessing()

	processor.AddMetric(&d1)
	processor.AddMetric(&d2)

	// This wait is necessary to make sure both metrics have been added to the batch
	<-time.Tick(time.Millisecond * 10)
	// Sending time to the ticker channel will flush the batch.
	mts.tickerChan <- firstTime
	firstBatch := <-mc.batches
	mts.now = secondTime

	processor.AddMetric(&d3)
	processor.AddMetric(&d4)

	processor.FinishProcessing()
	secondBatch := <-mc.batches
	batches := [][]APIMetric{firstBatch, secondBatch}

	assert.Equal(t, [][]APIMetric{
		[]APIMetric{
			{
				Name:       "metric-1",
				Tags:       []string{"a", "b", "c"},
				MetricType: DistributionType,
				Points: []interface{}{
					[]interface{}{firstTimeUnix, []interface{}{float64(1)}},
					[]interface{}{firstTimeUnix, []interface{}{float64(2)}},
					[]interface{}{firstTimeUnix, []interface{}{float64(3)}},
				},
			}},
		[]APIMetric{
			{
				Name:       "metric-1",
				Tags:       []string{"a", "b", "c"},
				MetricType: DistributionType,
				Points: []interface{}{
					[]interface{}{secondTimeUnix, []interface{}{float64(4)}},
					[]interface{}{secondTimeUnix, []interface{}{float64(5)}},
					[]interface{}{secondTimeUnix, []interface{}{float64(6)}},
				},
			}},
	}, batches)
}

func TestProcessorPerformsRetry(t *testing.T) {
	mc := makeMockClient()
	mts := makeMockTimeService()

	mts.now, _ = time.Parse(time.RFC3339, "2006-01-02T15:04:05Z")

	shouldRetry := true
	processor := MakeProcessor(context.Background(), &mc, &mts, 1000, shouldRetry, time.Hour*1000, time.Hour*1000, math.MaxUint32)

	d1 := Distribution{
		Name:   "metric-1",
		Tags:   []string{"a", "b", "c"},
		Values: []MetricValue{{Timestamp: mts.now, Value: 1}, {Timestamp: mts.now, Value: 2}, {Timestamp: mts.now, Value: 3}},
	}

	mc.err = errors.New("Some error")

	processor.AddMetric(&d1)

	processor.FinishProcessing()

	assert.Equal(t, 3, mc.sendMetricsCalledCount)
}

func TestProcessorCancelsWithContext(t *testing.T) {
	mc := makeMockClient()
	mts := makeMockTimeService()

	mts.now, _ = time.Parse(time.RFC3339, "2006-01-02T15:04:05Z")

	shouldRetry := true
	ctx, cancelFunc := context.WithCancel(context.Background())
	processor := MakeProcessor(ctx, &mc, &mts, 1000, shouldRetry, time.Hour*1000, time.Hour*1000, math.MaxUint32)

	d1 := Distribution{
		Name:   "metric-1",
		Tags:   []string{"a", "b", "c"},
		Values: []MetricValue{{Timestamp: mts.now, Value: 1}, {Timestamp: mts.now, Value: 2}, {Timestamp: mts.now, Value: 3}},
	}

	processor.AddMetric(&d1)
	// After calling cancelFunc, no metrics should be processed/sent
	cancelFunc()
	//<-time.Tick(time.Millisecond * 100)

	processor.FinishProcessing()

	assert.Equal(t, 0, mc.sendMetricsCalledCount)
}

func TestProcessorBatchesWithOpeningCircuitBreaker(t *testing.T) {
	mc := makeMockClient()
	mts := makeMockTimeService()

	mts.now, _ = time.Parse(time.RFC3339, "2006-01-02T15:04:05Z")

	// Will open the circuit breaker at number of total failures > 1
	circuitBreakerTotalFailures := uint32(1)
	processor := MakeProcessor(context.Background(), &mc, &mts, 1000, false, time.Hour*1000, time.Hour*1000, circuitBreakerTotalFailures)

	d1 := Distribution{
		Name:   "metric-1",
		Tags:   []string{"a", "b", "c"},
		Values: []MetricValue{{Timestamp: mts.now, Value: 1}, {Timestamp: mts.now, Value: 2}, {Timestamp: mts.now, Value: 3}},
	}

	mc.err = errors.New("Some error")

	processor.AddMetric(&d1)

	processor.FinishProcessing()

	// It should have retried 3 times, but circuit breaker opened at the second time
	assert.Equal(t, 1, mc.sendMetricsCalledCount)
}
