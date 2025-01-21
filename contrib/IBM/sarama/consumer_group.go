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

// WrapConsumerGroupHandler wraps a sarama.ConsumerGroupHandler causing each received
// message to be traced.
func WrapConsumerGroupHandler(handler sarama.ConsumerGroupHandler, opts ...Option) sarama.ConsumerGroupHandler {
	cfg := new(config)
	defaults(cfg)
	for _, opt := range opts {
		opt.apply(cfg)
	}
	instr.Logger().Debug("contrib/IBM/sarama: Wrapping Consumer Group Handler: %#v", cfg)

	return &consumerGroupHandler{
		ConsumerGroupHandler: handler,
		cfg:                  cfg,
	}
}

type consumerGroupClaim struct {
	sarama.ConsumerGroupClaim
	dispatcher dispatcher
}

func (c *consumerGroupClaim) Messages() <-chan *sarama.ConsumerMessage {
	return c.dispatcher.Messages()
}
