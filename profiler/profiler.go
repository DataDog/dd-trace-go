// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.

package profiler

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"sync"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

// outChannelSize specifies the size of the profile output channel.
const outChannelSize = 5

var (
	mu             sync.Mutex
	activeProfiler *profiler
	containerID    = internal.ContainerID() // replaced in tests
)

// Start starts the profiler. It may return an error if an API key is not provided by means of
// the WithAPIKey option, or if a hostname is not found.
func Start(opts ...Option) error {
	mu.Lock()
	if activeProfiler != nil {
		activeProfiler.stop()
	}
	p, err := newProfiler(opts...)
	if err != nil {
		return err
	}
	activeProfiler = p
	activeProfiler.run()
	mu.Unlock()
	return nil
}

// Stop stops the profiler.
func Stop() {
	mu.Lock()
	if activeProfiler != nil {
		activeProfiler.stop()
		activeProfiler = nil
	}
	mu.Unlock()
}

// profiler collects and sends preset profiles to the Datadog API at a given frequency
// using a given configuration.
type profiler struct {
	cfg        *config           // profile configuration
	out        chan batch        // upload queue
	uploadFunc func(batch) error // defaults to (*profiler).upload; replaced in tests
	exit       chan struct{}     // exit signals the profiler to stop; it is closed after stopping
	stopOnce   sync.Once         // stopOnce ensures the profiler is stopped exactly once.
	wg         sync.WaitGroup    // wg waits for all goroutines to exit when stopping.
}

// newProfiler creates a new, unstarted profiler.
func newProfiler(opts ...Option) (*profiler, error) {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(cfg)
	}
	if cfg.apiKey != "" {
		if !isAPIKeyValid(cfg.apiKey) {
			return nil, errors.New("API key has incorrect format")
		}
		cfg.targetURL = cfg.apiURL
	} else {
		cfg.targetURL = cfg.agentURL
	}
	if cfg.hostname == "" {
		hostname, err := os.Hostname()
		if err != nil {
			if cfg.targetURL == cfg.apiURL {
				return nil, fmt.Errorf("could not obtain hostname: %v; try specifying it using profiler.WithHostname", err)
			}
			log.Warn("unable to look up hostname: %v", err)
		}
		cfg.hostname = hostname
	}
	p := profiler{
		cfg:  cfg,
		out:  make(chan batch, outChannelSize),
		exit: make(chan struct{}),
	}
	p.uploadFunc = p.upload
	return &p, nil
}

// run runs the profiler.
func (p *profiler) run() {
	if _, ok := p.cfg.types[MutexProfile]; ok {
		runtime.SetMutexProfileFraction(p.cfg.mutexFraction)
	}
	if _, ok := p.cfg.types[BlockProfile]; ok {
		runtime.SetBlockProfileRate(p.cfg.blockRate)
	}
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		tick := time.NewTicker(p.cfg.period)
		defer tick.Stop()
		p.collect(tick.C)
	}()
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		p.send()
	}()
}

// collect runs the profile types found in the configuration whenever the ticker receives
// an item.
func (p *profiler) collect(ticker <-chan time.Time) {
	defer close(p.out)
	for {
		select {
		case <-ticker:
			now := time.Now().UTC()
			bat := batch{
				host:  p.cfg.hostname,
				start: now,
				end:   now.Add(p.cfg.cpuDuration), // abstraction violation
			}
			for t := range p.cfg.types {
				prof, err := p.runProfile(t)
				if err != nil {
					log.Error("Error getting %s profile: %v; skipping.\n", t, err)
					p.cfg.statsd.Count("datadog.profiler.go.collect_error", 1, append(p.cfg.tags, fmt.Sprintf("profile_type:%v", t)), 1)
					continue
				}
				bat.addProfile(prof)
			}
			p.enqueueUpload(bat)
		case <-p.exit:
			return
		}
	}
}

// enqueueUpload pushes a batch of profiles onto the queue to be uploaded. If there is no room, it will
// evict the oldest profile to make some. Typically a batch would be one of each enabled profile.
func (p *profiler) enqueueUpload(bat batch) {
	for {
		select {
		case p.out <- bat:
			return // 👍
		default:
			// queue is full; evict oldest
			select {
			case <-p.out:
				p.cfg.statsd.Count("datadog.profiler.go.queue_full", 1, p.cfg.tags, 1)
				log.Warn("Evicting one profile batch from the upload queue to make room.\n")
			default:
				// queue is empty; contents likely got uploaded
			}
		}
	}
}

// send takes profiles from the output queue and uploads them.
func (p *profiler) send() {
	for bat := range p.out {
		if err := p.uploadFunc(bat); err != nil {
			log.Error("Failed to upload profile: %v\n", err)
		}
	}
}

// stop stops the profiler.
func (p *profiler) stop() {
	p.stopOnce.Do(func() {
		close(p.exit)
	})
	p.wg.Wait()
}

// StatsdClient implementations can count and time certain event occurrences that happen
// in the profiler.
type StatsdClient interface {
	// Count counts how many times an event happened, at the given rate using the given tags.
	Count(event string, times int64, tags []string, rate float64) error
	// Timing creates a distribution of the values registered as the duration of a certain event.
	Timing(event string, duration time.Duration, tags []string, rate float64) error
}
