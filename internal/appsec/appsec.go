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
)

type (
	Config struct {
		AgentURL          string
		Service           ServiceConfig
		Tags              []string
		Hostname          string
		MaxBatchLen       int
		MaxBatchStaleTime time.Duration
		Version           string
	}

	ServiceConfig struct {
		Name, Version, Environment string
	}
)

// Default batching configuration values.
const (
	defaultMaxBatchLen       = 1024
	defaultMaxBatchStaleTime = time.Second
)

// Default timeout of intake requests.
const defaultIntakeTimeout = 10 * time.Second

type Agent struct {
	client             *intake.Client
	eventChan          chan *appsectypes.SecurityEvent
	wg                 sync.WaitGroup
	cfg                *Config
	instrumentationIDs []dyngo.EventListenerID
}

type Logger interface {
	Warn(string, ...interface{})
	Error(string, ...interface{})
}

func NewAgent(client *http.Client, logger Logger, cfg *Config) (*Agent, error) {
	intakeClient, err := intake.NewClient(client, cfg.AgentURL)
	if err != nil {
		return nil, err
	}

	if cfg.Hostname == "" {
		hostname, err := os.Hostname()
		if err != nil {
			logger.Warn("unable to look up hostname: %v", err)
		} else {
			cfg.Hostname = hostname
		}
	}

	if cfg.Service.Name == "" {
		name, err := os.Executable()
		if err != nil {
			logger.Warn("unable to look up the executable name: %v", err)
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

func (a *Agent) Start() {
	a.run()
}

func (a *Agent) Stop(gracefully bool) {
	dyngo.Unregister(a.instrumentationIDs)
	// Stop the batching goroutine
	close(a.eventChan)
	// Possibly wait for the goroutine to gracefully stop
	if !gracefully {
		return
	}
	a.wg.Wait()
}

func (a *Agent) run() {
	a.instrumentationIDs = append(a.instrumentationIDs, httpprotection.Register()...)
	a.instrumentationIDs = append(a.instrumentationIDs, a.listenSecurityEvents()...)

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

type IntakeClient interface {
	SendBatch(context.Context, api.EventBatch) error
}

func eventBatchingLoop(client IntakeClient, eventChan <-chan *appsectypes.SecurityEvent, globalEventCtx []appsectypes.SecurityEventContext, cfg *Config) {
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

func (a *Agent) listenSecurityEvents() []dyngo.EventListenerID {
	return dyngo.Register(dyngo.InstrumentationDescriptor{
		Title: "Attack Queue",
		Instrumentation: dyngo.OperationInstrumentation{
			EventListener: dyngo.OnDataEventListener(func(_ *dyngo.Operation, event *appsectypes.SecurityEvent) {
				select {
				case a.eventChan <- event:
				default:
					// TODO: add metrics on the nb of dropped events
				}
			}),
		},
	})

}
