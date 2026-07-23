// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/locking"
)

const (
	processRetryControlVersion       = 1
	processRetryControlFrameMaxBytes = 4 * 1024

	processRetryControlAttemptReady        = "attempt_ready"
	processRetryControlAdmissionRequest    = "body_admission_request"
	processRetryControlBodyAdmitted        = "body_admitted"
	processRetryControlRunBody             = "run_body"
	processRetryControlParallelRequest     = "parallel_request"
	processRetryControlParallelResume      = "parallel_resume"
	processRetryControlTerminalReady       = "controlled_terminal_ready"
	processRetryControlTerminalCommit      = "controlled_terminal_commit"
	processRetryControlTerminalCommitted   = "controlled_terminal_committed"
	processRetryControlAbort               = "abort"
	processRetryControlTransportUnixPipes  = "unix_pipes"
	processRetryControlTransportWinHandles = "windows_handles"
)

var (
	errProcessRetryControlMissing  = errors.New("process retry control configuration missing")
	errProcessRetryControlInvalid  = errors.New("process retry control protocol invalid")
	errProcessRetryControlTooLarge = errors.New("process retry control frame too large")
)

type processRetryControlConfig struct {
	Version                int    `json:"version"`
	Transport              string `json:"transport"`
	TestName               string `json:"test_name"`
	Attempt                int    `json:"attempt"`
	RetryReason            string `json:"retry_reason"`
	MRunEpoch              uint64 `json:"m_run_epoch,omitempty"`
	InvocationOrdinal      uint64 `json:"invocation_ordinal,omitempty"`
	ReadEndpoint           uint64 `json:"read_endpoint"`
	WriteEndpoint          uint64 `json:"write_endpoint"`
	ParentDeadlineUnixNano int64  `json:"parent_deadline_unix_nano"`
	ParentDeadlineOK       bool   `json:"parent_deadline_ok"`
	ObservedGOMAXPROCS     int    `json:"observed_gomaxprocs"`
}

type processRetryControlFrame struct {
	Version           int    `json:"version"`
	TestName          string `json:"test_name"`
	Attempt           int    `json:"attempt"`
	RetryReason       string `json:"retry_reason"`
	MRunEpoch         uint64 `json:"m_run_epoch,omitempty"`
	InvocationOrdinal uint64 `json:"invocation_ordinal,omitempty"`
	Sequence          uint64 `json:"sequence"`
	Kind              string `json:"kind"`
	Reason            string `json:"reason,omitempty"`
}

type processRetryControlTransport struct {
	read       *os.File
	write      *os.File
	childRead  *os.File
	childWrite *os.File
	config     processRetryControlConfig
}

type processRetryControl struct {
	cfg       processRetryChildConfig
	read      *os.File
	write     *os.File
	reader    *bufio.Reader
	sendMu    locking.Mutex
	sendSeq   uint64
	recvSeq   uint64
	childIn   *os.File
	childOut  *os.File
	close     sync.Once
	stateMu   locking.Mutex
	terminal  processRetryControlledTerminalState
	serveDone chan struct{}
	wire      processRetryControlConfig
}

type processRetryControlledTerminalState struct {
	status    processRetryStatus
	ready     bool
	committed bool
}

func processRetryControlConfigPath(resultPath string) string {
	return filepath.Clean(resultPath) + ".control.json"
}

func newParentProcessRetryControl(cmd *exec.Cmd, cfg processRetryChildConfig) (*processRetryControl, error) {
	transport, err := prepareProcessRetryControlTransport(cmd)
	if err != nil {
		return nil, err
	}
	control := &processRetryControl{
		cfg:      cfg,
		read:     transport.read,
		write:    transport.write,
		reader:   bufio.NewReaderSize(transport.read, processRetryControlFrameMaxBytes),
		childIn:  transport.childRead,
		childOut: transport.childWrite,
	}
	transport.config.Version = processRetryControlVersion
	transport.config.TestName = cfg.TestName
	transport.config.Attempt = cfg.Attempt
	transport.config.RetryReason = cfg.RetryReason
	transport.config.MRunEpoch = cfg.MRunEpoch
	transport.config.InvocationOrdinal = cfg.InvocationOrdinal
	transport.config.ParentDeadlineUnixNano = cfg.ParentDeadlineUnixNano
	transport.config.ParentDeadlineOK = cfg.ParentDeadlineOK
	transport.config.ObservedGOMAXPROCS = cfg.ObservedGOMAXPROCS
	if transport.config.ObservedGOMAXPROCS < 1 {
		transport.config.ObservedGOMAXPROCS = processRetryCurrentCPU()
	}
	control.wire = transport.config
	if err := writeProcessRetryControlConfig(processRetryControlConfigPath(cfg.ResultPath), transport.config); err != nil {
		_ = control.Close()
		return nil, err
	}
	return control, nil
}

func newChildProcessRetryControl(cfg processRetryChildConfig) (*processRetryControl, error) {
	controlCfg, err := readProcessRetryControlConfig(processRetryControlConfigPath(cfg.ResultPath), cfg)
	if err != nil {
		return nil, err
	}
	cfg = enrichProcessRetryChildConfig(cfg, controlCfg)
	read, write, err := openProcessRetryChildControlTransport(controlCfg)
	if err != nil {
		return nil, err
	}
	return &processRetryControl{
		cfg:    cfg,
		read:   read,
		write:  write,
		reader: bufio.NewReaderSize(read, processRetryControlFrameMaxBytes),
		wire:   controlCfg,
	}, nil
}

func (c *processRetryControl) CloseChildEndpoints() error {
	if c == nil {
		return nil
	}
	err := errors.Join(closeProcessRetryControlFile(c.childIn), closeProcessRetryControlFile(c.childOut))
	c.childIn = nil
	c.childOut = nil
	return err
}

func (c *processRetryControl) Close() error {
	if c == nil {
		return nil
	}
	var err error
	c.close.Do(func() {
		err = errors.Join(
			closeProcessRetryControlFile(c.childIn),
			closeProcessRetryControlFile(c.childOut),
			closeProcessRetryControlFile(c.read),
			closeProcessRetryControlFile(c.write),
		)
		c.childIn = nil
		c.childOut = nil
		c.read = nil
		c.write = nil
	})
	return err
}

func closeProcessRetryControlFile(file *os.File) error {
	if file == nil {
		return nil
	}
	return file.Close()
}

func (c *processRetryControl) Send(kind, reason string) error {
	if c == nil || c.write == nil {
		return errProcessRetryControlInvalid
	}
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	c.sendSeq++
	frame := processRetryControlFrame{
		Version:           processRetryControlVersion,
		TestName:          c.cfg.TestName,
		Attempt:           c.cfg.Attempt,
		RetryReason:       c.cfg.RetryReason,
		MRunEpoch:         c.cfg.MRunEpoch,
		InvocationOrdinal: c.cfg.InvocationOrdinal,
		Sequence:          c.sendSeq,
		Kind:              kind,
		Reason:            reason,
	}
	payload, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	if len(payload)+1 > processRetryControlFrameMaxBytes {
		return errProcessRetryControlTooLarge
	}
	payload = append(payload, '\n')
	for len(payload) > 0 {
		n, writeErr := c.write.Write(payload)
		if writeErr != nil {
			return writeErr
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		payload = payload[n:]
	}
	return nil
}

func (c *processRetryControl) Receive() (processRetryControlFrame, error) {
	if c == nil || c.reader == nil {
		return processRetryControlFrame{}, errProcessRetryControlInvalid
	}
	payload, err := c.reader.ReadSlice('\n')
	if errors.Is(err, bufio.ErrBufferFull) {
		return processRetryControlFrame{}, errProcessRetryControlTooLarge
	}
	if err != nil {
		return processRetryControlFrame{}, err
	}
	if len(payload) > processRetryControlFrameMaxBytes || len(payload) == 0 || payload[len(payload)-1] != '\n' {
		return processRetryControlFrame{}, errProcessRetryControlTooLarge
	}
	payload = payload[:len(payload)-1]
	var frame processRetryControlFrame
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&frame); err != nil {
		return processRetryControlFrame{}, errors.Join(errProcessRetryControlInvalid, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return processRetryControlFrame{}, errProcessRetryControlInvalid
	}
	if frame.Version != processRetryControlVersion ||
		frame.TestName != c.cfg.TestName ||
		frame.Attempt != c.cfg.Attempt ||
		frame.RetryReason != c.cfg.RetryReason ||
		frame.MRunEpoch != c.cfg.MRunEpoch ||
		frame.InvocationOrdinal != c.cfg.InvocationOrdinal ||
		frame.Sequence != c.recvSeq+1 ||
		!validProcessRetryControlKind(frame.Kind) ||
		len(frame.Reason) > processRetryResultErrorMaxBytes {
		return processRetryControlFrame{}, errProcessRetryControlInvalid
	}
	c.recvSeq = frame.Sequence
	return frame, nil
}

func (c *processRetryControl) Expect(kind string) error {
	frame, err := c.Receive()
	if err != nil {
		return err
	}
	if frame.Kind != kind {
		return fmt.Errorf("%w: expected %s, received %s", errProcessRetryControlInvalid, kind, frame.Kind)
	}
	return nil
}

func (c *processRetryControl) expect(kind, reason string) error {
	frame, err := c.Receive()
	if err != nil {
		return err
	}
	if frame.Kind != kind || frame.Reason != reason {
		return fmt.Errorf("%w: expected %s/%s, received %s/%s", errProcessRetryControlInvalid, kind, reason, frame.Kind, frame.Reason)
	}
	return nil
}

func (c *processRetryControl) parentAdmission(
	ctx context.Context,
	shutdown <-chan struct{},
	timeout <-chan time.Time,
	waitCh <-chan error,
) (admitted bool, childExited bool, waitErr error, controlErr error) {
	type admissionResult struct {
		admitted bool
		err      error
	}
	done := make(chan admissionResult, 1)
	go func() {
		if err := c.Expect(processRetryControlAttemptReady); err != nil {
			done <- admissionResult{err: err}
			return
		}
		if err := c.Send(processRetryControlAdmissionRequest, ""); err != nil {
			done <- admissionResult{err: err}
			return
		}
		if err := c.Expect(processRetryControlBodyAdmitted); err != nil {
			done <- admissionResult{err: err}
			return
		}
		done <- admissionResult{admitted: true, err: c.Send(processRetryControlRunBody, "")}
	}()
	select {
	case result := <-done:
		if errors.Is(result.err, io.EOF) {
			select {
			case err := <-waitCh:
				return false, true, err, nil
			case <-ctx.Done():
				return false, false, nil, ctx.Err()
			case <-shutdown:
				return false, false, nil, errProcessRetryShutdown
			case <-timeout:
				return false, false, nil, context.DeadlineExceeded
			}
		}
		return result.admitted, false, nil, result.err
	case err := <-waitCh:
		return false, true, err, nil
	case <-ctx.Done():
		return false, false, nil, ctx.Err()
	case <-shutdown:
		return false, false, nil, errProcessRetryShutdown
	case <-timeout:
		return false, false, nil, context.DeadlineExceeded
	}
}

func (c *processRetryControl) serveParent(group *retryAttemptGroup) <-chan error {
	errorsCh := make(chan error, 1)
	done := make(chan struct{})
	c.stateMu.Lock()
	c.serveDone = done
	c.stateMu.Unlock()
	go func() {
		defer close(done)
		defer close(errorsCh)
		for {
			frame, err := c.Receive()
			if err != nil {
				if !errors.Is(err, io.EOF) && !errors.Is(err, os.ErrClosed) {
					errorsCh <- err
				}
				return
			}
			switch frame.Kind {
			case processRetryControlParallelRequest:
				if group != nil {
					group.transitionOriginalToParallel()
				}
				if err := c.Send(processRetryControlParallelResume, ""); err != nil {
					errorsCh <- err
					return
				}
			case processRetryControlTerminalReady:
				status := processRetryStatus(frame.Reason)
				if !isProcessRetryControlledTerminalStatus(status) {
					errorsCh <- errProcessRetryControlInvalid
					return
				}
				result, _, resultErr := readProcessRetryResult(c.cfg.ResultPath, c.cfg)
				if resultErr != nil || result.Status != status {
					errorsCh <- errors.Join(errProcessRetryControlInvalid, resultErr)
					return
				}
				c.stateMu.Lock()
				if c.terminal.ready {
					c.stateMu.Unlock()
					errorsCh <- errProcessRetryControlInvalid
					return
				}
				c.terminal.status = status
				c.terminal.ready = true
				c.stateMu.Unlock()
				if err := c.Send(processRetryControlTerminalCommit, frame.Reason); err != nil {
					errorsCh <- err
					return
				}
				if err := c.expect(processRetryControlTerminalCommitted, frame.Reason); err != nil {
					errorsCh <- err
					return
				}
				c.stateMu.Lock()
				c.terminal.committed = true
				c.stateMu.Unlock()
			case processRetryControlAbort:
				if frame.Reason == "testmain_multiple_m_run" {
					errorsCh <- errProcessRetryMultipleMRun
				} else {
					errorsCh <- errProcessRetryControlInvalid
				}
				return
			default:
				errorsCh <- errProcessRetryControlInvalid
				return
			}
		}
	}()
	return errorsCh
}

func (c *processRetryControl) childAdmission() error {
	if err := c.Send(processRetryControlAttemptReady, ""); err != nil {
		return err
	}
	if err := c.Expect(processRetryControlAdmissionRequest); err != nil {
		return err
	}
	if err := c.Send(processRetryControlBodyAdmitted, ""); err != nil {
		return err
	}
	return c.Expect(processRetryControlRunBody)
}

func (c *processRetryControl) childRootParallelBridge() error {
	if err := c.Send(processRetryControlParallelRequest, ""); err != nil {
		return err
	}
	return c.Expect(processRetryControlParallelResume)
}

func (c *processRetryControl) logicalDeadline() (time.Time, bool) {
	if c == nil || !c.wire.ParentDeadlineOK {
		return time.Time{}, false
	}
	wall := time.Unix(0, c.wire.ParentDeadlineUnixNano)
	now := time.Now()
	return now.Add(wall.Sub(now)), true
}

func (c *processRetryControl) childControlledTerminal(status processRetryStatus) error {
	if !isProcessRetryControlledTerminalStatus(status) {
		return errProcessRetryControlInvalid
	}
	reason := string(status)
	if err := c.Send(processRetryControlTerminalReady, reason); err != nil {
		return err
	}
	if err := c.expect(processRetryControlTerminalCommit, reason); err != nil {
		return err
	}
	return c.Send(processRetryControlTerminalCommitted, reason)
}

func (c *processRetryControl) controlledTerminalState() processRetryControlledTerminalState {
	if c == nil {
		return processRetryControlledTerminalState{}
	}
	c.stateMu.Lock()
	done := c.serveDone
	c.stateMu.Unlock()
	if done != nil {
		<-done
	}
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	return c.terminal
}

func validProcessRetryControlKind(kind string) bool {
	switch kind {
	case processRetryControlAttemptReady,
		processRetryControlAdmissionRequest,
		processRetryControlBodyAdmitted,
		processRetryControlRunBody,
		processRetryControlParallelRequest,
		processRetryControlParallelResume,
		processRetryControlTerminalReady,
		processRetryControlTerminalCommit,
		processRetryControlTerminalCommitted,
		processRetryControlAbort:
		return true
	default:
		return false
	}
}

func writeProcessRetryControlConfig(path string, cfg processRetryControlConfig) error {
	if err := validateProcessRetryControlConfig(cfg, processRetryChildConfig{
		TestName:          cfg.TestName,
		Attempt:           cfg.Attempt,
		RetryReason:       cfg.RetryReason,
		MRunEpoch:         cfg.MRunEpoch,
		InvocationOrdinal: cfg.InvocationOrdinal,
	}); err != nil {
		return err
	}
	payload, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	if len(payload) > processRetryResultMaxBytes {
		return errProcessRetryControlTooLarge
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".process-retry-control-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	closed := false
	defer func() {
		if !closed {
			_ = tmp.Close()
		}
		_ = os.Remove(tmpName)
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := tmp.Write(payload); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		closed = true
		return err
	}
	closed = true
	return os.Rename(tmpName, path)
}

func readProcessRetryControlConfig(path string, expected processRetryChildConfig) (processRetryControlConfig, error) {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return processRetryControlConfig{}, errProcessRetryControlMissing
		}
		return processRetryControlConfig{}, err
	}
	defer file.Close()
	payload, err := io.ReadAll(io.LimitReader(file, processRetryResultMaxBytes+1))
	if err != nil {
		return processRetryControlConfig{}, err
	}
	if len(payload) > processRetryResultMaxBytes {
		return processRetryControlConfig{}, errProcessRetryControlTooLarge
	}
	var cfg processRetryControlConfig
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return processRetryControlConfig{}, errors.Join(errProcessRetryControlInvalid, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return processRetryControlConfig{}, errProcessRetryControlInvalid
	}
	if err := validateProcessRetryControlConfig(cfg, expected); err != nil {
		return processRetryControlConfig{}, err
	}
	return cfg, nil
}

func validateProcessRetryControlConfig(cfg processRetryControlConfig, expected processRetryChildConfig) error {
	if cfg.Version != processRetryControlVersion ||
		cfg.TestName != expected.TestName ||
		cfg.Attempt != expected.Attempt ||
		cfg.RetryReason != expected.RetryReason ||
		(expected.MRunEpoch != 0 && cfg.MRunEpoch != expected.MRunEpoch) ||
		(expected.InvocationOrdinal != 0 && cfg.InvocationOrdinal != expected.InvocationOrdinal) ||
		strings.TrimSpace(cfg.TestName) == "" || cfg.Attempt < 1 || strings.TrimSpace(cfg.RetryReason) == "" ||
		cfg.ReadEndpoint == 0 || cfg.WriteEndpoint == 0 || cfg.ReadEndpoint == cfg.WriteEndpoint {
		return errProcessRetryControlInvalid
	}
	if (cfg.MRunEpoch == 0) != (cfg.InvocationOrdinal == 0) {
		return errProcessRetryControlInvalid
	}
	if cfg.ObservedGOMAXPROCS < 1 || (!cfg.ParentDeadlineOK && cfg.ParentDeadlineUnixNano != 0) {
		return errProcessRetryControlInvalid
	}
	switch cfg.Transport {
	case processRetryControlTransportUnixPipes, processRetryControlTransportWinHandles:
		return nil
	default:
		return errProcessRetryControlInvalid
	}
}
