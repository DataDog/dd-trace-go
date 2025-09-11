// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package metrics

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/DataDog/dd-trace-go/v2/contrib/aws/datadog-lambda-go/internal/logger"
	"github.com/cenkalti/backoff/v4"
	"github.com/sony/gobreaker"
)

type (
	// Processor is used to batch metrics on a background thread, and send them on to a client periodically.
	Processor interface {
		// AddMetric sends a metric to the agent
		AddMetric(metric Metric)
		// StartProcessing begins processing metrics asynchronously
		StartProcessing()
		// FinishProcessing shuts down the agent, and tries to flush any remaining metrics
		FinishProcessing()
		// Whether the processor is still processing
		IsProcessing() bool
	}

	processor struct {
		context           context.Context
		metricsChan       chan Metric
		timeService       TimeService
		waitGroup         sync.WaitGroup
		batchInterval     time.Duration
		client            Client
		batcher           *Batcher
		shouldRetryOnFail bool
		isProcessing      bool
		breaker           *gobreaker.CircuitBreaker
	}
)

// MakeProcessor creates a new metrics context
func MakeProcessor(ctx context.Context, client Client, timeService TimeService, batchInterval time.Duration, shouldRetryOnFail bool, circuitBreakerInterval time.Duration, circuitBreakerTimeout time.Duration, circuitBreakerTotalFailures uint32) Processor {
	batcher := MakeBatcher(batchInterval)

	breaker := MakeCircuitBreaker(circuitBreakerInterval, circuitBreakerTimeout, circuitBreakerTotalFailures)

	return &processor{
		context:           ctx,
		metricsChan:       make(chan Metric, 2000),
		batchInterval:     batchInterval,
		waitGroup:         sync.WaitGroup{},
		client:            client,
		batcher:           batcher,
		shouldRetryOnFail: shouldRetryOnFail,
		timeService:       timeService,
		isProcessing:      false,
		breaker:           breaker,
	}
}

func MakeCircuitBreaker(circuitBreakerInterval time.Duration, circuitBreakerTimeout time.Duration, circuitBreakerTotalFailures uint32) *gobreaker.CircuitBreaker {
	readyToTrip := func(counts gobreaker.Counts) bool {
		return counts.TotalFailures > circuitBreakerTotalFailures
	}

	st := gobreaker.Settings{
		Name:        "post distribution_points",
		Interval:    circuitBreakerInterval,
		Timeout:     circuitBreakerTimeout,
		ReadyToTrip: readyToTrip,
	}
	return gobreaker.NewCircuitBreaker(st)
}

func (p *processor) AddMetric(metric Metric) {
	// We use a large buffer in the metrics channel, to make this operation non-blocking.
	// However, if the channel does fill up, this will become a blocking operation.
	p.metricsChan <- metric
}

func (p *processor) StartProcessing() {
	if !p.isProcessing {
		p.isProcessing = true
		p.waitGroup.Add(1)
		go p.processMetrics()
	}

}

func (p *processor) FinishProcessing() {
	if !p.isProcessing {
		p.StartProcessing()
	}
	// Closes the metrics channel, and waits for the last send to complete
	close(p.metricsChan)
	p.waitGroup.Wait()
}

func (p *processor) IsProcessing() bool {
	return p.isProcessing
}

func (p *processor) processMetrics() {

	ticker := p.timeService.NewTicker(p.batchInterval)

	doneChan := p.context.Done()
	shouldExit := false
	for !shouldExit {
		shouldSendBatch := false
		// Batches metrics until timeout is reached
		select {
		case <-doneChan:
			// This process is being cancelled by the context,(probably due to a lambda deadline), exit without flushing.
			shouldExit = true
		case m, ok := <-p.metricsChan:
			if !ok {
				// The channel has now been closed
				shouldSendBatch = true
				shouldExit = true
			} else {
				p.batcher.AddMetric(m)
			}
		case <-ticker.C:
			// We are ready to send a batch to our backend
			shouldSendBatch = true
		}
		// Since the go select statement picks randomly if multiple values are available, it's possible the done channel was
		// closed, but another channel was selected instead. We double check the done channel, to make sure this isn't he case.
		select {
		case <-doneChan:
			shouldExit = true
			shouldSendBatch = false
		default:
			// Non-blocking
		}

		if shouldSendBatch {
			_, err := p.breaker.Execute(func() (interface{}, error) {
				if shouldExit && p.shouldRetryOnFail {
					// If we are shutting down, and we just failed to send our last batch, do a retry
					bo := backoff.WithMaxRetries(backoff.NewConstantBackOff(defaultRetryInterval), 2)
					err := backoff.Retry(p.sendMetricsBatch, bo)
					if err != nil {
						return nil, fmt.Errorf("after retry: %v", err)
					}
				} else {
					err := p.sendMetricsBatch()
					if err != nil {
						return nil, fmt.Errorf("with no retry: %v", err)
					}
				}
				return nil, nil
			})
			if err != nil {
				logger.Error(fmt.Errorf("failed to flush metrics to datadog API: %v", err))
			}
		}
	}
	ticker.Stop()
	p.isProcessing = false
	p.waitGroup.Done()
}

func (p *processor) sendMetricsBatch() error {
	mts := p.batcher.ToAPIMetrics()
	if len(mts) > 0 {
		oldBatcher := p.batcher
		p.batcher = MakeBatcher(p.batchInterval)

		err := p.client.SendMetrics(mts)
		if err != nil {
			if p.shouldRetryOnFail {
				// If we want to retry on error, keep the metrics in the batcher until they are sent correctly.
				p.batcher = oldBatcher
			}
			return err
		}
	}
	return nil
}
