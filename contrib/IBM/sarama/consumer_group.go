// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sarama

import (
	"github.com/IBM/sarama"
)

type consumerGroupHandler struct {
	sarama.ConsumerGroupHandler
	cfg *config
	// cancelFetch cancels the cluster ID fetch, if one was started.
	cancelFetch func()
}

func (h *consumerGroupHandler) ConsumeClaim(session sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	// Wrap claim
	wd := wrapDispatcher(claim, h.cfg)
	go wd.Run()
	claim = &consumerGroupClaim{
		ConsumerGroupClaim: claim,
		dispatcher:         wd,
	}

	return h.ConsumerGroupHandler.ConsumeClaim(session, claim)
}

// Cleanup cancels the cluster ID fetch if it is still running, and calls the
// wrapped handler's Cleanup. sarama calls Cleanup at the end of every session,
// so the fetch is cancelled at the end of the first one at the latest. Cleanup
// does not wait for the fetch: sarama commits the session's offsets after
// Cleanup, and a broker request in progress can take until its network timeout.
// The fetch uses a connection of its own, so it can finish later.
func (h *consumerGroupHandler) Cleanup(session sarama.ConsumerGroupSession) error {
	if h.cancelFetch != nil {
		h.cancelFetch()
	}
	return h.ConsumerGroupHandler.Cleanup(session)
}

// WrapConsumerGroupHandler wraps a sarama.ConsumerGroupHandler causing each received
// message to be traced.
func WrapConsumerGroupHandler(handler sarama.ConsumerGroupHandler, opts ...Option) sarama.ConsumerGroupHandler {
	cfg := new(config)
	defaults(cfg)
	for _, opt := range opts {
		opt.apply(cfg)
	}
	instr.Logger().Debug("contrib/IBM/sarama: Wrapping Consumer Group Handler: %#v", cfg)

	wrapped := &consumerGroupHandler{
		ConsumerGroupHandler: handler,
		cfg:                  cfg,
	}
	if cfg.dataStreamsEnabled && len(cfg.brokerAddrs) > 0 {
		wrapped.cancelFetch, _ = launchClusterIDFetch(cfg)
	}
	return wrapped
}

type consumerGroupClaim struct {
	sarama.ConsumerGroupClaim
	dispatcher dispatcher
}

func (c *consumerGroupClaim) Messages() <-chan *sarama.ConsumerMessage {
	return c.dispatcher.Messages()
}
