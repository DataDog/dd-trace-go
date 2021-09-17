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

const (
	defaultMaxBatchLen       = 1024
	defaultMaxBatchStaleTime = time.Second
)

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

func (a *Agent) Start(ctx context.Context) {
	a.run(ctx)
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

func (a *Agent) run(ctx context.Context) {
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

		eventBatchingLoop(ctx, a.client, a.eventChan, globalEventCtx, a.cfg)
	}()
}

type IntakeClient interface {
	SendBatch(context.Context, api.EventBatch) error
}

func eventBatchingLoop(ctx context.Context, client IntakeClient, eventChan <-chan *appsectypes.SecurityEvent, globalEventCtx []appsectypes.SecurityEventContext, cfg *Config) {
	batch := make([]*appsectypes.SecurityEvent, 0, cfg.MaxBatchLen)
	timer := time.NewTimer(time.Hour)
	timer.Stop()

	for {
		select {
		case <-ctx.Done():
			// Return immediately as the context is now done so the http client cannot be used anymore
			return

		case event, ok := <-eventChan:
			if event != nil {
				batch = append(batch, event)
			}
			if !ok {
				timer.Stop()
				if len(batch) > 0 {
					client.SendBatch(ctx, api.FromSecurityEvents(batch, globalEventCtx))
				}
				return
			}

			if l := len(batch); l == cfg.MaxBatchLen {
				timer.Stop()
				client.SendBatch(ctx, api.FromSecurityEvents(batch, globalEventCtx))
				batch = batch[0:0]
			} else if l == 1 {
				timer = time.NewTimer(cfg.MaxBatchStaleTime)
			}

		case <-timer.C:
			if len(batch) == 0 {
				continue
			}
			client.SendBatch(ctx, api.FromSecurityEvents(batch, globalEventCtx))
			batch = batch[0:0]
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
