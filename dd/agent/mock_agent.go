package agent

import (
	"errors"
	"io"
	"strings"
	"sync/atomic"
)

type MockAgent struct {
	cfg               mockCfg
	TraceSendAttempts uint32
	TraceSendSuccess  uint32
	StatsSendAttempts uint32
	StatsSendSuccess  uint32
}

type mockCfg struct {
	failCount int
}

type MockOpt func(cfg *mockCfg)

// WithFailCount causes the first n submissions to return an error.
func WithFailCount(n int) MockOpt {
	return func(cfg *mockCfg) {
		cfg.failCount = n
	}
}

func NewMock(opts ...MockOpt) *MockAgent {
	cfg := mockCfg{}
	for _, o := range opts {
		o(&cfg)
	}
	return &MockAgent{cfg: cfg}
}

func (a *MockAgent) SubmitStats(p io.Reader) error {
	atomic.AddUint32(&a.StatsSendAttempts, 1)
	if a.cfg.failCount > 0 {
		a.cfg.failCount--
		return errors.New("SubmitStats failed in MockAgent.")
	}
	atomic.AddUint32(&a.StatsSendSuccess, 1)
	return nil
}

func (a *MockAgent) SubmitTraces(p io.Reader, headers map[string]string) (body io.ReadCloser, err error) {
	atomic.AddUint32(&a.TraceSendAttempts, 1)
	if a.cfg.failCount > 0 {
		a.cfg.failCount--
		return nil, errors.New("SubmitTraces failed in MockAgent.")
	}
	atomic.AddUint32(&a.TraceSendSuccess, 1)
	return io.NopCloser(strings.NewReader("OK")), nil
}
