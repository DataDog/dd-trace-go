// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package internal

import (
	"sync"
	"sync/atomic"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

type StatsdClient interface {
	Incr(name string, tags []string, rate float64) error
	Count(name string, value int64, tags []string, rate float64) error
	Gauge(name string, value float64, tags []string, rate float64) error
	Timing(name string, value time.Duration, tags []string, rate float64) error
	Flush() error
	Close() error
}

type Stat struct {
	Name  string
	Kind  string
	Value interface{} //Really, it can only be a "number" type, but wasn't sure if we want to enforce that here yet
	Tags  []string
	Rate  float64
}

// StatsCarrier collects stats on its contribStats channel and submits them to the Datadog agent via a statsd client
type StatsCarrier struct {
	contribStats chan Stat
	statsd       StatsdClient
	stop         chan struct{}
	wg           sync.WaitGroup
	stopped uint64
}

func NewStatsCarrier(statsd StatsdClient) *StatsCarrier {
	return &StatsCarrier{
		contribStats: make(chan Stat),
		statsd:       statsd,
		stopped: 1,
	}
}
func (sc *StatsCarrier) Start() {
	if atomic.SwapUint64(&sc.stopped, 0) == 0 {
		// already running
		log.Warn("(*StatsCarrier).Start called more than once. This is likely a programming error.")
		return
	}
	sc.stop = make(chan struct{})
	sc.wg.Add(1)
	go func() {
		defer sc.wg.Done()
		sc.run()
	}()
}

func (sc *StatsCarrier) run() {
	for {
		select {
		case stat := <-sc.contribStats:
			sc.push(stat)
		case <-sc.stop:
			// make sure to flush any stats still in the channel
			if len(sc.contribStats) > 0 {
				sc.push(<-sc.contribStats)
			}
			return
		}
	}
}

func (sc *StatsCarrier) Stop() {	
	if atomic.SwapUint64(&(sc.stopped), 1) > 0 {
		return
	}
	close(sc.stop)
	sc.wg.Wait()
}

// push submits the stat of supported types (gauge or count) via its statsd client
func (sc *StatsCarrier) push(s Stat) {
	switch s.Kind {
	case "gauge":
		v, ok := s.Value.(float64)
		if !ok {
			log.Debug("Received gauge stat with incompatible value; looking for float64 value but got %T. Dropping stat %v.", s.Value, s.Name)
			break
		}
		sc.statsd.Gauge(s.Name, v, s.Tags, s.Rate)
	case "count":
		v, ok := s.Value.(int64)
		if !ok {
			log.Debug("Received count stat with incompatible value; looking for int64 value but got %T. Dropping stat %v.", s.Value, s.Name)
			break
		}
		sc.statsd.Count(s.Name, v, s.Tags, s.Rate)
	default:
		log.Debug("Stat submission failed: metric type %v not supported", s.Kind)
	}
}

func (sc *StatsCarrier) Add(s Stat) {
	sc.contribStats <- s
}
