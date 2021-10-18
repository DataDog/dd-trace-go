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

	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

// Default batching configuration values.
const (
	defaultMaxBatchLen       = 1024
	defaultMaxBatchStaleTime = time.Second
)

// Default timeout of intake requests.
const defaultIntakeTimeout = 10 * time.Second

// Start AppSec when enabled is enabled by both using the appsec build tag and
// setting the environment variable DD_APPSEC_ENABLED to true.
func Start(cfg *Config) {
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
		log.Info("appsec: using rules from file %s", filepath)
	} else {
		log.Info("appsec: using the default recommended rules")
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

// isEnabled returns true when appsec is enabled by both using the appsec build
// tag and having the environment variable DD_APPSEC_ENABLED set to true.
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
	client        *client
	eventChan     chan securityEvent
	wg            sync.WaitGroup
	cfg           *Config
	unregisterWAF dyngo.UnregisterFunc
}

func newAppSec(cfg *Config) (*appsec, error) {
	intakeClient, err := newClient(cfg.Client, cfg.AgentURL)
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
		eventChan: make(chan securityEvent, 1000),
		client:    intakeClient,
		cfg:       cfg,
	}, nil
}

// Start starts the AppSec background goroutine.
func (a *appsec) start() error {
	// Register the WAF operation event listener
	unregisterWAF, err := registerWAF(a.cfg.rules, a)
	if err != nil {
		return err
	}
	a.unregisterWAF = unregisterWAF

	// Start the background goroutine reading the channel of security events and sending them to the backend
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()

		strTags := stringTags(a.cfg.Tags)
		osName := fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH)
		applyContext := func(event securityEvent) securityEvent {
			if len(strTags) > 0 {
				event = withTagsContext(event, strTags)
			}
			event = withServiceContext(event, a.cfg.Service.Name, a.cfg.Service.Version, a.cfg.Service.Environment)
			event = withTracerContext(event, "go", runtime.Version(), a.cfg.Version)
			event = withHostContext(event, a.cfg.Hostname, osName)
			return event
		}
		eventBatchingLoop(a.client, a.eventChan, applyContext, a.cfg)
	}()

	return nil
}

func stringTags(tagsMap map[string]interface{}) (tags []string) {
	tags = make([]string, 0, len(tagsMap))
	for tag, value := range tagsMap {
		if str, ok := value.(string); ok {
			tags = append(tags, fmt.Sprintf("%s:%v", tag, str))
		}
	}
	return tags
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
	sendBatch(context.Context, eventBatch) error
}

func eventBatchingLoop(client intakeClient, eventChan <-chan securityEvent, withGlobalContext func(event securityEvent) securityEvent, cfg *Config) {
	// The batch of events
	batch := make([]securityEvent, 0, cfg.MaxBatchLen)

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
		intakeBatch := make([]*attackEvent, 0, len(batch))
		for _, e := range batch {
			intakeEvents, err := withGlobalContext(e).toIntakeEvents()
			if err != nil {
				log.Error("appsec: could not create intake security events: %v", err)
				continue
			}
			intakeBatch = append(intakeBatch, intakeEvents...)
		}
		log.Debug("appsec: sending %d security events", len(intakeBatch))
		if err := client.sendBatch(ctx, makeEventBatch(intakeBatch)); err != nil {
			log.Error("appsec: could not send the event batch: %v", err)
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

func (a *appsec) sendEvent(event securityEvent) {
	select {
	case a.eventChan <- event:
	default:
		// TODO(julio): add metrics on the nb of dropped events
	}
}
