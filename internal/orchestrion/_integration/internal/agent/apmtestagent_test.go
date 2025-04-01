// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package agent

import (
	"bytes"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMonitorReady(t *testing.T) {
	t.Run("successful", func(t *testing.T) {
		var out bytes.Buffer
		w, rdy := newMonitorReady(&out)

		for chunk := range slices.Chunk([]byte(successfulStart), 5) {
			n, err := w.Write(chunk)
			require.NoError(t, err)
			require.Len(t, chunk, n)
		}

		assert.Equal(t, successfulStart, out.String())
		assert.True(t, <-rdy)
	})

	t.Run("failing", func(t *testing.T) {
		var out bytes.Buffer
		w, rdy := newMonitorReady(&out)

		for chunk := range slices.Chunk([]byte(errorStart), 5) {
			n, err := w.Write(chunk)
			require.NoError(t, err)
			require.Len(t, chunk, n)
		}

		assert.Equal(t, errorStart, out.String())
		assert.False(t, <-rdy)
	})
}

// Sample output from ddapm-test-agent when it's starting up okay...
const successfulStart = `INFO:ddapm_test_agent.agent:Trace request stall seconds setting set to 0.0.
WARNING:ddapm_test_agent.agent:default snapshot directory '/Datadog/dd-trace-go/snapshots' does not exist or is not readable. Snapshotting will not work.
======== Running on http://0.0.0.0:8126 ========
(Press CTRL+C to quit)`

// Sample output from ddapm-test-agent when it's starting up with an error...
const errorStart = `INFO:ddapm_test_agent.agent:Trace request stall seconds setting set to 0.0.
WARNING:ddapm_test_agent.agent:default snapshot directory '/Datadog/appsec-internal-go/snapshots' does not exist or is not readable. Snapshotting will not work.
Traceback (most recent call last):
  File "/bin/ddapm-test-agent", line 8, in <module>
    sys.exit(main())
  File "/lib/python3.10/site-packages/ddapm_test_agent/agent.py", line 1321, in main
    web.run_app(app, sock=apm_sock, port=parsed_args.port)
  File "/lib/python3.10/site-packages/aiohttp/web.py", line 526, in run_app
    loop.run_until_complete(main_task)
  File "/lib/python3.10/asyncio/base_events.py", line 649, in run_until_complete
    return future.result()
OSError: [Errno 48] error while attempting to bind on address ('0.0.0.0', 8126): address already in use`
