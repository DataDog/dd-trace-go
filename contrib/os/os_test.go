// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package os_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/DataDog/go-libddwaf/v5"
	"github.com/DataDog/go-libddwaf/v5/timer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/appsec/events"
	wrapos "github.com/DataDog/dd-trace-go/v2/contrib/os"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/dyngo"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/ossec"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/waf/addresses"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/config"
	lfi "github.com/DataDog/dd-trace-go/v2/internal/appsec/listener/ossec"
)

func TestCmdiDetectorVectors(t *testing.T) {
	if wafOk, err := libddwaf.Usable(); !wafOk {
		t.Skipf("WAF must be usable for this test to run correctly: %v", err)
	}

	_, testFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	rules, err := os.ReadFile(filepath.Join(filepath.Dir(testFile), "..", "..", "internal", "orchestrion", "_integration", "testdata", "rasp-only-rules.json"))
	require.NoError(t, err)

	manager, err := config.NewWAFManagerWithStaticRules(config.ObfuscatorConfig{}, rules)
	require.NoError(t, err)
	defer manager.Close()

	handle, _ := manager.NewHandle()
	require.NotNil(t, handle)
	defer handle.Close()

	cases := []struct {
		name      string
		resource  []string
		param     string
		wantMatch bool
	}{
		{name: "no injection unrelated reboot", resource: []string{"/usr/bin/reboot", "-f"}, param: "whatever"},
		{name: "no injection unrelated ls", resource: []string{"ls", "-l", "/file/in/repository"}, param: "whatever"},
		{name: "no injection unrelated shell command", resource: []string{"/usr/bin/ash", "-c", "ls -l $file ; cat /etc/passwd"}, param: "whatever"},
		{name: "no executable injection basename only", resource: []string{"/usr/bin/reboot"}, param: "reboot"},
		{name: "no executable injection unrelated executable", resource: []string{"/usr/bin/reboot", "-f"}, param: "unrelated.exe"},
		{name: "no executable injection path prefix", resource: []string{"/usr/bin/reboot", "-f"}, param: "/usr"},
		{name: "no executable injection path segment", resource: []string{"/usr/bin/reboot", "-f"}, param: "usr"},
		{name: "no executable injection argument", resource: []string{"/usr/bin/reboot", "-f"}, param: "-f"},
		{name: "no shell injection partial command", resource: []string{"/usr/bin/ash", "-c", "ls -l $file ; cat /etc/passwd"}, param: "cat"},
		{name: "no shell injection shell substring", resource: []string{"/bin/fish", "ls -l -r -t"}, param: "-r -t"},
		{name: "executable injection full path", resource: []string{"/usr/bin/reboot"}, param: "/usr/bin/reboot", wantMatch: true},
		{name: "executable injection full path with args", resource: []string{"/usr/bin/reboot", "-f"}, param: "/usr/bin/reboot", wantMatch: true},
		{name: "executable injection repeated separators", resource: []string{"//usr//bin//reboot", "-f"}, param: "//usr//bin//reboot", wantMatch: true},
		{name: "executable injection relative bin path", resource: []string{"bin/reboot", "-f"}, param: "bin/reboot", wantMatch: true},
		{name: "executable injection parent relative path", resource: []string{"../reboot", "-f"}, param: "../reboot", wantMatch: true},
		{name: "executable injection root path", resource: []string{"/reboot", "-f"}, param: "/reboot", wantMatch: true},
		{name: "executable injection normalized traversal text", resource: []string{"/bin/../usr/bin/reboot", "-f"}, param: "/bin/../usr/bin/reboot", wantMatch: true},
		{name: "executable with spaces trims resource and param", resource: []string{"   /usr/bin/ls         ", "-l", "/file/in/repository"}, param: " /usr/bin/ls ", wantMatch: true},
		{name: "executable with spaces trims newline", resource: []string{"  /usr/bin/ls\n", "-l", "/file/in/repository"}, param: " /usr/bin/ls\n", wantMatch: true},
		{name: "shell injection sh command", resource: []string{"/bin/sh", "-c", "ls -l"}, param: "ls -l", wantMatch: true},
		{name: "shell injection bash command", resource: []string{"/usr/bin/bash", "-c", "--", "ls -l"}, param: "ls -l", wantMatch: true},
		{name: "shell injection fish command equals", resource: []string{"/usr/bin/fish", "--command=ls -l"}, param: "ls -l", wantMatch: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wafCtx, err := handle.NewContext(context.Background(), timer.WithBudget(time.Second), timer.WithComponents(addresses.Scopes[:]...))
			require.NoError(t, err)
			defer wafCtx.Close()

			runData := addresses.NewAddressesBuilder().
				WithQuery(map[string][]string{"cmd": {tc.param}}).
				WithSysExecCmd(tc.resource).
				Build()

			result, err := wafCtx.Run(context.Background(), runData)
			require.NoError(t, err)
			require.NotNil(t, result)
			assert.Equalf(t, tc.wantMatch, result.HasEvents(), "resource=%q param=%q", tc.resource, tc.param)
			assert.Equalf(t, tc.wantMatch, result.HasActions(), "resource=%q param=%q", tc.resource, tc.param)
		})
	}
}

func TestOpenFile(t *testing.T) {
	ctx := context.Background()
	rootOp := dyngo.NewRootOperation()
	feature, err := lfi.NewOSSecFeature(
		&config.Config{
			RASP:               true,
			SupportedAddresses: map[string]struct{}{addresses.ServerIOFSFileAddr: {}},
		},
		rootOp,
	)
	require.NoError(t, err)
	defer feature.Stop()

	ctx = dyngo.RegisterOperation(ctx, rootOp)
	dyngo.On(rootOp, func(op *ossec.OpenOperation, args ossec.OpenOperationArgs) {
		// We shall block this request!
		dyngo.EmitData(op, &events.BlockingSecurityEvent{})

		assert.Equal(t, "/etc/passwd", args.Path)
		assert.Equal(t, os.O_RDONLY, args.Flags)
		assert.Equal(t, os.FileMode(0), args.Perms)
	})

	file, err := wrapos.OpenFile(ctx, "/etc/passwd", os.O_RDONLY, 0)
	require.ErrorContains(t, err, "blocked")
	require.Nil(t, file)
}

func TestStartProcess(t *testing.T) {
	t.Run("blocking", func(t *testing.T) {
		cases := []struct {
			name string
			argv []string
		}{
			{name: "/usr/bin/reboot", argv: []string{"/usr/bin/reboot", "-f"}},
			{name: "/usr/bin/reboot", argv: []string{"/usr/bin/reboot"}},
			{name: "bin/reboot", argv: []string{"bin/reboot", "-f"}},
			{name: "//usr//bin//reboot", argv: []string{"//usr//bin//reboot", "-f"}},
			{name: "../reboot", argv: []string{"../reboot", "-f"}},
			{name: "/usr/bin/ls", argv: []string{"/usr/bin/ls", "-l", "/file/in/repository"}},
			{name: "/usr/bin/bash", argv: []string{"/usr/bin/bash", "-c", "ls -l ; $(cat /etc/passwd)"}},
			{name: "C:/bin/powershell.exe", argv: []string{"C:/bin/powershell.exe", "-Command", "ls -l"}},
			{name: "/usr/bin/wget", argv: []string{"ls", "-la"}},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				ctx := context.Background()
				rootOp := dyngo.NewRootOperation()
				feature, err := lfi.NewExecSecFeature(
					&config.Config{
						RASP:               true,
						SupportedAddresses: map[string]struct{}{addresses.ServerSysExecCmd: {}},
					},
					rootOp,
				)
				require.NoError(t, err)
				defer feature.Stop()

				ctx = dyngo.RegisterOperation(ctx, rootOp)
				dyngo.On(rootOp, func(op *ossec.RunCommandOperation, args ossec.RunCommandOperationArgs) {
					// Blocking happens before os.StartProcess, so these attack samples are not executed.
					dyngo.EmitData(op, &events.BlockingSecurityEvent{})

					assert.Equal(t, tc.name, args.Name)
					assert.Equal(t, tc.argv, args.Commands)
				})

				proc, err := wrapos.StartProcess(ctx, tc.name, tc.argv, &os.ProcAttr{})
				require.ErrorContains(t, err, "blocked")
				require.True(t, events.IsSecurityError(err))
				require.Nil(t, proc)
			})
		}
	})

	t.Run("non-blocking", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("self-exec pass-through test is Unix-only")
		}

		ctx := context.Background()
		rootOp := dyngo.NewRootOperation()
		feature, err := lfi.NewExecSecFeature(
			&config.Config{
				RASP:               true,
				SupportedAddresses: map[string]struct{}{addresses.ServerSysExecCmd: {}},
			},
			rootOp,
		)
		require.NoError(t, err)
		defer feature.Stop()

		ctx = dyngo.RegisterOperation(ctx, rootOp)

		exe, err := os.Executable()
		require.NoError(t, err)

		proc, err := wrapos.StartProcess(ctx, exe, []string{exe, "-test.run=^$"}, &os.ProcAttr{})
		require.NoError(t, err)
		require.NotNil(t, proc)
		// Reap the child to avoid leaking a process.
		_, _ = proc.Wait()
	})
}
