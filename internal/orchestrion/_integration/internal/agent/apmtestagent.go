// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package agent

import (
	"bytes"
	"io"
	"strings"
)

type monitorReady struct {
	w     io.Writer
	ready chan bool
	buf   bytes.Buffer
	done  bool
}

// newMonitorReady returns a new [io.Writer] that forwards writes to the
// provided [w], and a channel that'll receive `true` when the ddapm-test-agent
// is ready to serve; or `false` if EOF is reached before then.
func newMonitorReady(w io.Writer) (io.Writer, chan bool) {
	ready := make(chan bool, 1)
	return &monitorReady{w: w, ready: ready}, ready
}

func (m *monitorReady) Write(p []byte) (int, error) {
	if !m.done {
		for _, b := range p {
			if b != '\n' {
				if err := m.buf.WriteByte(b); err != nil {
					return 0, err
				}
				continue
			}
			// We reached a newline, so we'll check the current line for readiness...
			line := m.buf.String()
			m.buf.Reset()
			if strings.Contains(line, "Running on http://") {
				// We're done here... we found the readiness line!
				m.done = true
				m.ready <- true
				close(m.ready)
				break
			}
			if strings.HasPrefix(line, "Traceback") {
				// We have encountered a start error... we're done, but not successful!
				m.done = true
				close(m.ready)
				break
			}
		}
	}
	return m.w.Write(p)
}
