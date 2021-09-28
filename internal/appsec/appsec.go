// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package appsec

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/internal/intake"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/internal/intake/api"
	httpprotection "gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/internal/protection/http"
	appsectypes "gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/types"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

type (
	// Config is the AppSec configuration.
	Config struct {
		// AgentURL is the datadog agent URL the API client should use.
		AgentURL string
		// ServiceConfig is the information about the running service we currently protect.
		Service ServiceConfig
		// Tags is the list of tags that should be added to security events (eg. pid, os name, etc.).
		Tags []string
		// Hostname of the machine we run in.
		Hostname string
		// Version of the Go client library
		Version string

		// MaxBatchLen is the maximum batch length the event batching loop should use. The event batch is sent when
		// this length is reached. Defaults to 1024.
		MaxBatchLen int
		// MaxBatchStaleTime is the maximum amount of time events are kept in the batch. This allows to send the batch
		// after this amount of time even if the maximum batch length is not reached yet. Defaults to 1 second.
		MaxBatchStaleTime time.Duration
	}

	// ServiceConfig is the optional context about the running service.
	ServiceConfig struct {
		// Name of the service.
		Name string
		// Version of the service.
		Version string
		// Environment of the service (eg. dev, staging, prod, etc.)
		Environment string
	}
)

// Default batching configuration values.
const (
	defaultMaxBatchLen       = 1024
	defaultMaxBatchStaleTime = time.Second
)

// Default timeout of intake requests.
const defaultIntakeTimeout = 10 * time.Second

// Agent is the AppSec agent. It allows starting and stopping it.
type Agent struct {
	client          *intake.Client
	eventChan       chan *appsectypes.SecurityEvent
	wg              sync.WaitGroup
	cfg             *Config
	unregisterInstr []dyngo.UnregisterFunc
}

// NewAgent returns a new unstarted agent.
func NewAgent(client *http.Client, cfg *Config) (*Agent, error) {
	intakeClient, err := intake.NewClient(client, cfg.AgentURL)
	if err != nil {
		return nil, err
	}

	if cfg.Hostname == "" {
		hostname, err := os.Hostname()
		if err != nil {
			log.Warn("unable to look up hostname: %v", err)
		} else {
			cfg.Hostname = hostname
		}
	}

	if cfg.Service.Name == "" {
		name, err := os.Executable()
		if err != nil {
			log.Warn("unable to look up the executable name: %v", err)
		} else {
			cfg.Service.Name = filepath.Base(name)
		}
	}

	if cfg.MaxBatchLen <= 0 {
		cfg.MaxBatchLen = defaultMaxBatchLen
	}

	if cfg.MaxBatchStaleTime <= 0 {
		cfg.MaxBatchStaleTime = defaultMaxBatchStaleTime
	}

	return &Agent{
		eventChan: make(chan *appsectypes.SecurityEvent, 1000),
		client:    intakeClient,
		cfg:       cfg,
	}, nil
}

// Start starts the AppSec agent goroutine.
func (a *Agent) Start() {
	a.run()
}

// Stop stops the AppSec agent goroutine.
func (a *Agent) Stop() {
	for _, unregister := range a.unregisterInstr {
		unregister()
	}
	// Stop the batching goroutine
	close(a.eventChan)
	// Gracefully stop by waiting for the event loop goroutine to stop
	a.wg.Wait()
}

func (a *Agent) run() {
	a.unregisterInstr = append(a.unregisterInstr, httpprotection.Register(), a.listenSecurityEvents())

	a.wg.Add(1)
	go func() {
		defer a.wg.Done()

		globalEventCtx := []appsectypes.SecurityEventContext{
			appsectypes.ServiceContext{
				Name:        a.cfg.Service.Name,
				Version:     a.cfg.Service.Version,
				Environment: a.cfg.Service.Environment,
			},
			appsectypes.TagContext(a.cfg.Tags),
			appsectypes.TracerContext{
				Runtime:        "go",
				RuntimeVersion: runtime.Version(),
				Version:        a.cfg.Version,
			},
			appsectypes.HostContext{
				Hostname: a.cfg.Hostname,
				OS:       fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
			},
		}

		eventBatchingLoop(a.client, a.eventChan, globalEventCtx, a.cfg)
	}()
}

type intakeClient interface {
	SendBatch(context.Context, api.EventBatch) error
}

func eventBatchingLoop(client intakeClient, eventChan <-chan *appsectypes.SecurityEvent, globalEventCtx []appsectypes.SecurityEventContext, cfg *Config) {
	// The batch of events
	batch := make([]*appsectypes.SecurityEvent, 0, cfg.MaxBatchLen)

	// Timer initialized to a first dummy time value to initialize it and so that we can immediately
	// use its channel field in the following select statement.
	timer := time.NewTimer(time.Hour)
	timer.Stop()

	// Helper function stopping the timer, sending the batch and resetting it.
	sendBatch := func() {
		if !timer.Stop() {
			// Remove the time value from the channel in case the timer fired and so that we avoid
			// sending the batch again in the next loop iteration due to a value in the timer
			// channel.
			select {
			case <-timer.C:
			default:
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), defaultIntakeTimeout)
		defer cancel()
		client.SendBatch(ctx, api.FromSecurityEvents(batch, globalEventCtx))
		batch = batch[0:0]
	}

	// Loop-select between the event channel or the stale timer (when enabled).
	for {
		select {
		case event, ok := <-eventChan:
			// Add the event to the batch.
			// The event might be nil when closing the channel while it was empty.
			if event != nil {
				batch = append(batch, event)
			}
			if !ok {
				// The event channel has been closed. Send the batch if it's not empty.
				if len(batch) > 0 {
					sendBatch()
				}
				return
			}
			// Send the batch when it's full or start the timer when this is the first value in
			// the batch.
			if l := len(batch); l == cfg.MaxBatchLen {
				sendBatch()
			} else if l == 1 {
				timer.Reset(cfg.MaxBatchStaleTime)
			}

		case <-timer.C:
			sendBatch()
		}
	}
}

func (a *Agent) listenSecurityEvents() dyngo.UnregisterFunc {
	return dyngo.Register(dyngo.InstrumentationDescriptor{
		Title: "Attack Queue",
		Instrumentation: dyngo.OperationInstrumentation{
			EventListener: appsectypes.OnSecurityEventDataListener(func(_ *dyngo.Operation, event *appsectypes.SecurityEvent) {
				select {
				case a.eventChan <- event:
				default:
					// TODO: add metrics on the nb of dropped events
				}
			}),
		},
	})

}
