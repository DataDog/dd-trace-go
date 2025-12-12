// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package logs

import (
	"sync"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/net"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
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

// logsWriter is responsible for writing logs to the agentless endpoint.
type logsWriter struct {
	client  net.Client     // http client
	payload *logsPayload   // Encodes and buffers events in msgpack format.
	climit  chan struct{}  // Limits the number of concurrent outgoing connections.
	wg      sync.WaitGroup // Waits for all uploads to finish.
}

// newLogsWriter creates a new instance of logsWriter.
func newLogsWriter() *logsWriter {
	log.Debug("logsWriter: creating logs writer instance")
	return &logsWriter{
		client:  net.NewClientForLogs(),
		payload: newLogsPayload(),
		climit:  make(chan struct{}, concurrentConnectionLimit),
	}
}

func (w *logsWriter) add(entry *logEntry) {
	if err := w.payload.push(entry); err != nil {
		log.Error("logsWriter: Error encoding msgpack: %s", err.Error())
	}
	if w.payload.size() > agentlessPayloadSizeLimit {
		w.flush()
	}
}

func (w *logsWriter) stop() {
	log.Debug("logsWriter: stopping writer")
	w.flush()
	w.wg.Wait()
}

func (w *logsWriter) flush() {
	if w.payload.itemCount() == 0 {
		return
	}

	w.wg.Add(1)
	w.climit <- struct{}{}
	oldp := w.payload
	w.payload = newLogsPayload()

	go func(p *logsPayload) {
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
		log.Debug("logsWriter: sending payload: size: %d logs entries: %d\n", size, count)

		err := w.client.SendLogs(p)
		if err != nil {
			log.Error("logsWriter: failure sending logs data data: %s", err.Error())
		}
	}(oldp)
}
