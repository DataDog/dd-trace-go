// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package coverage

import (
	"sync"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/utils/net"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/utils/telemetry"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

// Constants defining the payload size limits for agentless mode.
const (
	// agentlessPayloadMaxLimit is the maximum payload size allowed, indicating the
	// maximum size of the package that the intake can receive.
	agentlessPayloadMaxLimit = 50 * 1024 * 1024 // 5 MB

	// agentlessPayloadSizeLimit specifies the maximum allowed size of the payload before
	// it triggers a flush to the transport.
	agentlessPayloadSizeLimit = agentlessPayloadMaxLimit / 2

	// concurrentConnectionLimit specifies the maximum number of concurrent outgoing
	// connections allowed.
	concurrentConnectionLimit = 100
)

type coverageWriter struct {
	client  net.Client       // http client
	payload *coveragePayload // Encodes and buffers events in msgpack format.
	climit  chan struct{}    // Limits the number of concurrent outgoing connections.
	wg      sync.WaitGroup   // Waits for all uploads to finish.
}

func newCoverageWriter() *coverageWriter {
	log.Debug("coverageWriter: creating trace writer instance")
	return &coverageWriter{
		client:  net.NewClientForCodeCoverage(),
		payload: newCoveragePayload(),
		climit:  make(chan struct{}, concurrentConnectionLimit),
	}
}

func (w *coverageWriter) add(coverage *testCoverage) {
	telemetry.EventsEnqueueForSerialization()
	ciTestCoverage := newCiTestCoverageData(coverage)
	if err := w.payload.push(ciTestCoverage); err != nil {
		log.Error("coverageWriter: Error encoding msgpack: %v", err)
	}
	if w.payload.size() > agentlessPayloadSizeLimit {
		w.flush()
	}
}

func (w *coverageWriter) stop() {
	log.Debug("coverageWriter: stopping writer")
	w.flush()
	w.wg.Wait()
}

func (w *coverageWriter) flush() {
	if w.payload.itemCount() == 0 {
		return
	}

	w.wg.Add(1)
	w.climit <- struct{}{}
	oldp := w.payload
	w.payload = newCoveragePayload()

	go func(p *coveragePayload) {
		defer func() {
			// Once the payload has been used, clear the buffer for garbage
			// collection to avoid a memory leak when references to this object
			// may still be kept by faulty transport implementations or the
			// standard library. See dd-trace-go#976
			p.clear()

			<-w.climit
			w.wg.Done()
		}()

		size, count := p.size(), p.itemCount()
		log.Debug("coverageWriter: sending payload: size: %d events: %d\n", size, count)

		buf, err := p.getBuffer()
		if err != nil {
			log.Error("coverageWriter: failure getting coverage data: %v", err)
			return
		}

		telemetry.CodeCoverageFiles(float64(p.itemCount()))
		err = w.client.SendCoveragePayload(buf)
		if err != nil {
			log.Error("coverageWriter: failure sending coverage data: %v", err)
		}
	}(oldp)
}
