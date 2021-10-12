// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

//go:build appsec
// +build appsec

package appsec

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/internal/intake/api"
	appsectypes "gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/types"

	"github.com/stretchr/testify/mock"
)

type IntakeClientMock struct {
	mock.Mock
	SendBatchCalled chan struct{}
}

func (i *IntakeClientMock) SendBatch(ctx context.Context, batch api.EventBatch) error {
	err := i.Called(ctx, batch).Error(0)
	if i.SendBatchCalled != nil {
		i.SendBatchCalled <- struct{}{}
	}
	return err
}

func TestEventBatchingLoop(t *testing.T) {
	t.Run("batching", func(t *testing.T) {
		for _, eventChanLen := range []int{1, 2, 512, 1024} {
			eventChanLen := eventChanLen
			t.Run(fmt.Sprintf("EventChanLen=%d", eventChanLen), func(t *testing.T) {
				for _, maxBatchLen := range []int{1, 2, 512, 1024} {
					maxBatchLen := maxBatchLen
					t.Run(fmt.Sprintf("MaxBatchLen=%d", maxBatchLen), func(t *testing.T) {
						// Send 10 batches of events and check they were properly sent
						expectedNbBatches := 10
						client := &IntakeClientMock{
							// Have enough room for the amount of expected batches
							SendBatchCalled: make(chan struct{}, expectedNbBatches),
						}
						eventChan := make(chan *appsectypes.SecurityEvent, eventChanLen)
						cfg := &Config{
							MaxBatchLen:       maxBatchLen,
							MaxBatchStaleTime: time.Hour, // Long enough so that it never triggers and we only test the batching logic
						}

						// Start the batching goroutine
						var wg sync.WaitGroup
						wg.Add(1)
						go func() {
							defer wg.Done()
							eventBatchingLoop(client, eventChan, nil, cfg)
						}()

						client.On("SendBatch", mock.Anything, mock.AnythingOfType("api.EventBatch")).Times(expectedNbBatches).Return(nil)
						// Send enough events to generate expectedNbBatches
						for i := 0; i < maxBatchLen*expectedNbBatches; i++ {
							eventChan <- &appsectypes.SecurityEvent{}
						}
						// Sync with the client and check the client calls are being done as expected
						for i := 0; i < expectedNbBatches; i++ {
							<-client.SendBatchCalled
						}
						client.AssertExpectations(t)

						// Close the event channel to stop the loop
						close(eventChan)
						wg.Wait()
					})
				}
			})
		}
	})

	t.Run("stale time", func(t *testing.T) {
		client := &IntakeClientMock{
			SendBatchCalled: make(chan struct{}, 2),
		}
		eventChan := make(chan *appsectypes.SecurityEvent, 1024)
		maxStaleTime := time.Millisecond
		cfg := &Config{
			MaxBatchLen:       1024,
			MaxBatchStaleTime: maxStaleTime,
		}

		// Start the batching goroutine
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			eventBatchingLoop(client, eventChan, nil, cfg)
		}()

		//
		client.On("SendBatch", mock.Anything, mock.AnythingOfType("api.EventBatch")).Times(2).Return(nil)

		// Send a few events and wait for the configured max stale time so that the batch gets sent
		eventChan <- &appsectypes.SecurityEvent{}
		eventChan <- &appsectypes.SecurityEvent{}
		eventChan <- &appsectypes.SecurityEvent{}
		eventChan <- &appsectypes.SecurityEvent{}
		time.Sleep(maxStaleTime)
		// Sync with the client
		<-client.SendBatchCalled

		// Send a few events and wait for the configured max stale time so that the batch gets sent
		eventChan <- &appsectypes.SecurityEvent{}
		// Sync with the client
		<-client.SendBatchCalled
		time.Sleep(maxStaleTime)

		// No new events
		time.Sleep(maxStaleTime)

		// 2 batches should have been sent
		client.AssertExpectations(t)

		// Close the event channel to stop the loop
		close(eventChan)
		wg.Wait()
	})

	t.Run("canceling the loop", func(t *testing.T) {
		t.Run("by closing the event channel", func(t *testing.T) {
			t.Run("with an empty batch", func(t *testing.T) {
				client := &IntakeClientMock{}
				eventChan := make(chan *appsectypes.SecurityEvent, 1024)
				cfg := &Config{
					MaxBatchLen:       1024,
					MaxBatchStaleTime: time.Hour,
				}

				// Start the batching goroutine
				var wg sync.WaitGroup
				wg.Add(1)
				go func() {
					defer wg.Done()
					eventBatchingLoop(client, eventChan, nil, cfg)
				}()

				// No client calls should be made
				client.AssertExpectations(t)

				// Close the context to stop the loop
				close(eventChan)
				// Wait() should therefore return
				wg.Wait()
			})

			t.Run("with a non empty batch", func(t *testing.T) {
				client := &IntakeClientMock{
					SendBatchCalled: make(chan struct{}, 1),
				}
				eventChan := make(chan *appsectypes.SecurityEvent, 1024)
				cfg := &Config{
					MaxBatchLen:       1024,
					MaxBatchStaleTime: time.Hour,
				}

				// Start the batching goroutine
				var wg sync.WaitGroup
				wg.Add(1)
				go func() {
					defer wg.Done()
					eventBatchingLoop(client, eventChan, nil, cfg)
				}()

				// Perform an event
				client.On("SendBatch", mock.Anything, mock.AnythingOfType("api.EventBatch")).Times(1).Return(nil)
				eventChan <- &appsectypes.SecurityEvent{}

				// Close the context to stop the loop
				close(eventChan)

				// Wait() should therefore return
				wg.Wait()

				// The event should be properly sent before returning
				<-client.SendBatchCalled
				client.AssertExpectations(t)
			})
		})
	})
}
