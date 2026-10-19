// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package redigo

import (
	"bufio"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDialUnixSocket(t *testing.T) {
	// Unix socket paths are limited to about 100 bytes, which t.TempDir can
	// exceed on some systems, so use a short directory name.
	dir, err := os.MkdirTemp("", "redigo")
	require.NoError(t, err)
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "redis.sock")

	ln, err := net.Listen("unix", sock)
	require.NoError(t, err)
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		// Answer a single command, enough for the PING below.
		r := bufio.NewReader(conn)
		for range 3 { // *1, $4, PING
			if _, err := r.ReadString('\n'); err != nil {
				return
			}
		}
		conn.Write([]byte("+PONG\r\n"))
	}()

	mt := mocktracer.Start()
	defer mt.Stop()

	c, err := Dial("unix", sock)
	require.NoError(t, err)
	defer c.Close()

	res, err := c.Do("PING")
	require.NoError(t, err)
	assert.Equal(t, "PONG", res)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	span := spans[0]
	assert.Equal(t, "unix", span.Tag("out.network"))
	assert.Equal(t, sock, span.Tag(ext.TargetHost))
	assert.Nil(t, span.Tag(ext.TargetPort))
}
