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
	"io/ioutil"
	"os"
	"runtime"
	"strconv"
	"sync"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/internal/intake"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/internal/intake/api"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/internal/protection/waf"
	appsectypes "gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/types"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

// Default batching configuration values.
const (
	defaultMaxBatchLen       = 1024
	defaultMaxBatchStaleTime = time.Second
)

// Default timeout of intake requests.
const defaultIntakeTimeout = 10 * time.Second

// Start AppSec when the environment variable DD_APPSEC_ENABLED is set to true.
func Start(cfg *Config) (enabled bool) {
	enabled, err := isEnabled()
	if err != nil {
		log.Error("appsec: %v", err)
		return
	}
	if !enabled {
		return
	}

	filepath := os.Getenv("DD_APPSEC_RULES")
	if filepath != "" {
		rules, err := ioutil.ReadFile(filepath)
		if err != nil {
			if os.IsNotExist(err) {
				log.Error("appsec: could not find the rules file in path %s.\nAppSec will not run any protections in this application. No security activities will be collected: %v", filepath, err)
			} else {
				logUnexpectedStartError(err)
			}
			return
		}
		cfg.rules = rules
	}

	appsec, err := newAppSec(cfg)
	if err != nil {
		logUnexpectedStartError(err)
		return
	}
	if err := appsec.start(); err != nil {
		logUnexpectedStartError(err)
		return
	}
	setActiveAppSec(appsec)
	return true
}

// Implement the AppSec log message C1
func logUnexpectedStartError(err error) {
	log.Error("appsec: could not start because of an unexpected error. No security activities will be collected. Please contact support at https://docs.datadoghq.com/help/ for help:\n%v", err)
}

// Stop AppSec.
func Stop() {
	setActiveAppSec(nil)
}

var (
	activeAppSec *appsec
	mu           sync.Mutex
)

func setActiveAppSec(a *appsec) {
	mu.Lock()
	defer mu.Unlock()
	if activeAppSec != nil {
		activeAppSec.stop()
	}
	activeAppSec = a
}

func isEnabled() (bool, error) {
	enabledStr := os.Getenv("DD_APPSEC_ENABLED")
	if enabledStr == "" {
		return false, nil
	}
	enabled, err := strconv.ParseBool(enabledStr)
	if err != nil {
		return false, fmt.Errorf("could not parse DD_APPSEC_ENABLED value `%s` as a boolean value", enabledStr)
	}
	return enabled, nil
}

type appsec struct {
	client        *intake.Client
	eventChan     chan *appsectypes.SecurityEvent
	wg            sync.WaitGroup
	cfg           *Config
	unregisterWAF dyngo.UnregisterFunc
}

func newAppSec(cfg *Config) (*appsec, error) {
	intakeClient, err := intake.NewClient(cfg.Client, cfg.AgentURL)
	if err != nil {
		return nil, err
	}

	if cfg.MaxBatchLen <= 0 {
		cfg.MaxBatchLen = defaultMaxBatchLen
	}

	if cfg.MaxBatchStaleTime <= 0 {
		cfg.MaxBatchStaleTime = defaultMaxBatchStaleTime
	}

	return &appsec{
		eventChan: make(chan *appsectypes.SecurityEvent, 1000),
		client:    intakeClient,
		cfg:       cfg,
	}, nil
}

// Start starts the AppSec background goroutine.
func (a *appsec) start() error {
	// Register the WAF operation event listener
	unregisterWAF, err := waf.Register(a.cfg.rules, a)
	if err != nil {
		return err
	}
	a.unregisterWAF = unregisterWAF

	// Start the background goroutine reading the channel of security events and sending them to the backend
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

	return nil
}

// Stop stops the AppSec agent goroutine.
func (a *appsec) stop() {
	a.unregisterWAF()
	// Stop the batching goroutine
	close(a.eventChan)
	// Gracefully stop by waiting for the event loop goroutine to stop
	a.wg.Wait()
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
		log.Debug("appsec: sending %d events", len(batch))
		if err := client.SendBatch(ctx, api.FromSecurityEvents(batch, globalEventCtx)); err != nil {
			log.Debug("appsec: could not send the event batch: %v", err)
		}
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

func (a *appsec) SendEvent(event *appsectypes.SecurityEvent) {
	select {
	case a.eventChan <- event:
	default:
		// TODO(julio): add metrics on the nb of dropped events
	}
}
