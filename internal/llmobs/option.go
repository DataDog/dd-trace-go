package llmobs

import (
	"time"
)

// TODO(rarguelloF): add options

type startSpanConfig struct {
	sessionID     string
	modelName     string
	modelProvider string
	mlApp         string
	startTime     time.Time
}

type StartSpanOption func(cfg *startSpanConfig)

func WithSessionID(sessionID string) StartSpanOption {
	return func(cfg *startSpanConfig) {
		cfg.sessionID = sessionID
	}
}

func WithModelName(modelName string) StartSpanOption {
	return func(cfg *startSpanConfig) {
		cfg.modelName = modelName
	}
}

func WithModelProvider(modelProvider string) StartSpanOption {
	return func(cfg *startSpanConfig) {
		cfg.modelProvider = modelProvider
	}
}

func WithMLApp(mlApp string) StartSpanOption {
	return func(cfg *startSpanConfig) {
		cfg.mlApp = mlApp
	}
}

func WithStartTime(t time.Time) StartSpanOption {
	return func(cfg *startSpanConfig) {
		cfg.startTime = t
	}
}

//func WithTracerStartSpanOptions(opts ...Tracer.StartSpanOption) StartSpanOption {
//	return func(cfg *startSpanConfig) {
//		cfg.startSpanOpts = opts
//	}
//}

type finishSpanConfig struct {
	finishTime time.Time
	error      error
}

type FinishSpanOption func(cfg *finishSpanConfig)

func WithError(err error) FinishSpanOption {
	return func(cfg *finishSpanConfig) {
		cfg.error = err
	}
}

func WithFinishTime(t time.Time) FinishSpanOption {
	return func(cfg *finishSpanConfig) {
		cfg.finishTime = t
	}
}
