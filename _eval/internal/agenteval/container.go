// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package agenteval

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	containertypes "github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/client"
	"github.com/testcontainers/testcontainers-go"
)

const (
	containerWorkspace        = "/workspace"
	containerHome             = "/tmp/agent-home"
	defaultContainerCPUs      = int64(4)
	defaultContainerMemoryGiB = int64(4)
)

// ContainerResources sets the resource limits for one agent container.
type ContainerResources struct {
	CPUs      int64
	MemoryGiB int64
}

func (r ContainerResources) limits() (nanoCPUs, memoryBytes int64) {
	if r.CPUs <= 0 {
		r.CPUs = defaultContainerCPUs
	}
	if r.MemoryGiB <= 0 {
		r.MemoryGiB = defaultContainerMemoryGiB
	}
	return r.CPUs * 1_000_000_000, r.MemoryGiB << 30
}

type containerInvocation struct {
	Name        string
	Image       string
	Command     []string
	Environment map[string]string
	AuthMounts  []mount.Mount
	Resources   ContainerResources
}

type containerResult struct {
	ExitCode int
	TimedOut bool
}

func runInContainer(ctx context.Context, workspace, transcriptPath, stderrPath string, inv containerInvocation) (*containerResult, error) {
	if inv.Image == "" {
		return nil, fmt.Errorf("container image is required")
	}
	if len(inv.Command) == 0 {
		return nil, fmt.Errorf("container command is required")
	}

	uid, gid := os.Getuid(), os.Getgid()
	env := map[string]string{
		"EVAL_UID":   strconv.Itoa(uid),
		"EVAL_GID":   strconv.Itoa(gid),
		"HOME":       containerHome,
		"GOCACHE":    "/tmp/go-build",
		"GOMODCACHE": "/tmp/go-mod",
	}
	for key, value := range inv.Environment {
		env[key] = value
	}

	workspace, err := filepath.Abs(workspace)
	if err != nil {
		return nil, err
	}
	mounts := []mount.Mount{{
		Type:   mount.TypeBind,
		Source: workspace,
		Target: containerWorkspace,
	}}
	mounts = append(mounts, inv.AuthMounts...)

	transcript, err := os.Create(transcriptPath)
	if err != nil {
		return nil, err
	}
	defer transcript.Close()
	stderr, err := os.Create(stderrPath)
	if err != nil {
		return nil, err
	}
	defer stderr.Close()

	pids := int64(1024)
	init := true
	nanoCPUs, memoryBytes := inv.Resources.limits()
	opts := []testcontainers.ContainerCustomizer{
		testcontainers.WithCmd(inv.Command...),
		testcontainers.WithEnv(env),
		testcontainers.WithConfigModifier(func(cfg *containertypes.Config) {
			cfg.WorkingDir = containerWorkspace
			cfg.User = fmt.Sprintf("%d:%d", uid, gid)
		}),
		testcontainers.WithHostConfigModifier(func(cfg *containertypes.HostConfig) {
			cfg.ReadonlyRootfs = true
			cfg.CapDrop = []string{"ALL"}
			cfg.SecurityOpt = []string{"no-new-privileges:true"}
			cfg.Mounts = append(cfg.Mounts, mounts...)
			// Go executes compiled test binaries from GOCACHE, which lives under /tmp.
			cfg.Tmpfs = map[string]string{"/tmp": "rw,nosuid,nodev,size=8g,mode=1777"}
			cfg.PidsLimit = &pids
			cfg.Memory = memoryBytes
			cfg.NanoCPUs = nanoCPUs
			cfg.Init = &init
		}),
	}
	if inv.Name != "" {
		opts = append(opts, testcontainers.WithName(inv.Name))
	}
	// Pulling or starting the image can take minutes on a cold cache, so report
	// before blocking on it.
	progressf("container: starting %s from image %s", inv.Name, inv.Image)
	containerStarted := time.Now()
	ctr, err := testcontainers.Run(ctx, inv.Image, opts...)
	if err != nil {
		if ctr != nil {
			cleanupContainer(ctr)
		}
		return nil, fmt.Errorf("start agent container %s: %w", inv.Image, err)
	}
	defer cleanupContainer(ctr)
	progressf("container: %s up after %s, streaming transcript to %s",
		inv.Name, fmtDuration(time.Since(containerStarted)), transcriptPath)
	finishLogs := streamContainerLogs(ctr.GetContainerID(), transcript, stderr)
	defer finishLogs()

	timedOut := false
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		state, stateErr := ctr.State(ctx)
		if stateErr != nil {
			if ctx.Err() != nil {
				timedOut = true
				break
			}
			return nil, fmt.Errorf("inspect agent container: %w", stateErr)
		}
		if !state.Running {
			break
		}
		select {
		case <-ctx.Done():
			timedOut = true
			break
		case <-ticker.C:
			continue
		}
		break
	}

	if timedOut {
		stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		zero := time.Duration(0)
		_ = ctr.Stop(stopCtx, &zero)
		cancel()
	}
	if err := finishLogs(); err != nil {
		return nil, fmt.Errorf("write agent container logs: %w", err)
	}

	stateCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	state, err := ctr.State(stateCtx)
	cancel()
	if err != nil {
		return nil, fmt.Errorf("inspect completed agent container: %w", err)
	}
	exitCode := state.ExitCode
	if timedOut {
		exitCode = -1
	}
	return &containerResult{ExitCode: exitCode, TimedOut: timedOut}, nil
}

func streamContainerLogs(containerID string, transcript, stderr *os.File) func() error {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		dockerClient, err := client.New(client.FromEnv, client.WithAPIVersionNegotiation())
		if err != nil {
			done <- err
			return
		}
		defer dockerClient.Close()
		logs, err := dockerClient.ContainerLogs(ctx, containerID, client.ContainerLogsOptions{
			ShowStdout: true,
			ShowStderr: true,
			Follow:     true,
		})
		if err != nil {
			done <- err
			return
		}
		defer logs.Close()
		_, err = stdcopy.StdCopy(transcript, stderr, logs)
		if ctx.Err() != nil {
			err = nil
		}
		done <- err
	}()

	var once sync.Once
	var streamErr error
	return func() error {
		once.Do(func() {
			select {
			case streamErr = <-done:
				cancel()
			case <-time.After(5 * time.Second):
				cancel()
				select {
				case streamErr = <-done:
				case <-time.After(time.Second):
					streamErr = fmt.Errorf("timed out stopping Docker log stream")
				}
			}
		})
		return streamErr
	}
}

func agentContainerName(agent, artifactDir string) string {
	parts := make([]string, 0, 4)
	dir := filepath.Clean(artifactDir)
	for range 4 {
		parts = append(parts, filepath.Base(dir))
		dir = filepath.Dir(dir)
	}
	for left, right := 0, len(parts)-1; left < right; left, right = left+1, right-1 {
		parts[left], parts[right] = parts[right], parts[left]
	}

	raw := strings.Join(append([]string{"agent-eval", agent}, parts...), "-")
	var name strings.Builder
	previousDash := false
	for _, char := range strings.ToLower(raw) {
		allowed := char >= 'a' && char <= 'z' || char >= '0' && char <= '9'
		if allowed {
			name.WriteRune(char)
			previousDash = false
		} else if !previousDash {
			name.WriteByte('-')
			previousDash = true
		}
	}
	stem := strings.Trim(name.String(), "-")
	suffix := "-" + strconv.Itoa(os.Getpid())
	const maxNameLength = 128
	if len(stem)+len(suffix) > maxNameLength {
		stem = strings.TrimRight(stem[:maxNameLength-len(suffix)], "-")
	}
	return stem + suffix
}

func cleanupContainer(ctr *testcontainers.DockerContainer) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = ctr.Terminate(ctx)
}
