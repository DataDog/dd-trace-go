package llmobs

import (
	"sync"
	"time"
)

type Span struct {
	mu sync.RWMutex

	apm          APMSpan
	parent       *Span
	propagated   *PropagatedLLMSpan
	llmCtx       llmobsContext
	llmTraceID   string
	name         string
	mlApp        string
	integration  string
	scope        string
	isEvaluation bool
	error        error
	startTime    time.Time
	finishTime   time.Time
	spanLinks    []SpanLink
}

func (s *Span) SpanID() string {
	return s.apm.SpanID()
}

func (s *Span) APMTraceID() string {
	return s.apm.TraceID()
}

func (s *Span) LLMTraceID() string {
	return s.llmTraceID
}

func (s *Span) MLApp() string {
	return s.mlApp
}

func (s *Span) AddLink(link SpanLink) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.apm.AddLink(link)
	s.spanLinks = append(s.spanLinks, link)
}

func (s *Span) Finish(opts ...FinishSpanOption) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cfg := &finishSpanConfig{
		finishTime: time.Now(),
	}
	for _, opt := range opts {
		opt(cfg)
	}

	s.finishTime = cfg.finishTime
	apmFinishCfg := FinishAPMSpanConfig{
		FinishTime: cfg.finishTime,
	}
	if cfg.error != nil {
		s.error = cfg.error
		apmFinishCfg.Error = cfg.error
	}

	s.apm.Finish(apmFinishCfg)
	l, err := ActiveLLMObs()
	if err != nil {
		return
	}
	l.submitLLMObsSpan(s)

	//TODO: telemetry.record_span_created(span)
}

// sessionID returns the session ID for a given span, by checking the span's nearest LLMObs span ancestor.
func (s *Span) sessionID() string {
	curSpan := s

	for curSpan != nil {
		if curSpan.llmCtx.sessionID != "" {
			return curSpan.llmCtx.sessionID
		}
		curSpan = curSpan.parent
	}
	return ""
}

// propagatedMLApp returns the ML App name for a given span, by checking the span's nearest LLMObs span ancestor.
// It defaults to the global config LLMObs ML App name.
func (s *Span) propagatedMLApp() string {
	curSpan := s

	for curSpan != nil {
		if curSpan.mlApp != "" {
			return curSpan.mlApp
		}
		curSpan = curSpan.parent
	}

	if s.propagated != nil && s.propagated.MLApp != "" {
		return s.propagated.MLApp
	}
	if activeLLMObs != nil {
		return activeLLMObs.Config.MLApp
	}
	return ""
}

// isEvaluationSpan returns whether the current span or any of the parents is an evaluation span.
func (s *Span) isEvaluationSpan() bool {
	curSpan := s
	for curSpan != nil {
		if curSpan.isEvaluation {
			return true
		}
		curSpan = curSpan.parent
	}
	return false
}
