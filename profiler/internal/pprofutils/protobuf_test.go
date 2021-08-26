// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2021 Datadog, Inc.

package pprofutils

import (
	"bytes"
	"io/ioutil"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/pprof/profile"
	"github.com/stretchr/testify/require"
)

func TestProtobufConvert(t *testing.T) {
	t.Run("basic", func(t *testing.T) {
		data, err := ioutil.ReadFile(filepath.Join("test-fixtures", "pprof.samples.cpu.001.pb.gz"))
		require.NoError(t, err)

		proto, err := profile.Parse(bytes.NewReader(data))
		require.NoError(t, err)

		out := bytes.Buffer{}
		require.NoError(t, Protobuf{}.Convert(proto, &out))
		want := strings.TrimSpace(`
golang.org/x/sync/errgroup.(*Group).Go.func1;main.run.func2;main.computeSum 19
runtime.mcall;runtime.park_m;runtime.resetForSleep;runtime.resettimer;runtime.modtimer;runtime.wakeNetPoller;runtime.netpollBreak;runtime.write;runtime.write1 7
golang.org/x/sync/errgroup.(*Group).Go.func1;main.run.func2;main.computeSum;runtime.asyncPreempt 5
runtime.mstart;runtime.mstart1;runtime.sysmon;runtime.usleep 3
runtime.mcall;runtime.park_m;runtime.schedule;runtime.findrunnable;runtime.stopm;runtime.notesleep;runtime.semasleep;runtime.pthread_cond_wait 2
runtime.mcall;runtime.gopreempt_m;runtime.goschedImpl;runtime.schedule;runtime.findrunnable;runtime.stopm;runtime.notesleep;runtime.semasleep;runtime.pthread_cond_wait 1
runtime.mcall;runtime.park_m;runtime.schedule;runtime.findrunnable;runtime.checkTimers;runtime.nanotime;runtime.nanotime1 1
`) + "\n"
		require.Equal(t, out.String(), want)
	})

	t.Run("differentLinesPerFunction", func(t *testing.T) {
		data, err := ioutil.ReadFile(filepath.Join("test-fixtures", "pprof.lines.pb.gz"))
		require.NoError(t, err)

		proto, err := profile.Parse(bytes.NewReader(data))
		require.NoError(t, err)

		out := bytes.Buffer{}
		require.NoError(t, Protobuf{}.Convert(proto, &out))
		want := strings.TrimSpace(`
main.run.func1;main.threadKind.Run;main.goGo1;main.goHog 85
main.run.func1;main.threadKind.Run;main.goGo2;main.goHog 78
main.run.func1;main.threadKind.Run;main.goGo3;main.goHog 72
main.run.func1;main.threadKind.Run;main.goGo0;main.goHog 72
main.run.func1;main.threadKind.Run;main.goGo0;main.goHog;runtime.asyncPreempt 1
`) + "\n"
		require.Equal(t, out.String(), want)
	})
}
