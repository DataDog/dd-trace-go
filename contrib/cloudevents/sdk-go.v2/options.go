// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package sdkgov2

// Option is a functional option for configuring CloudEvents tracing.
type Option func(*config)

type config struct {
	includeSubject  bool
	resourceName    string
	messagingSystem string
}

func defaultConfig() *config {
	return &config{
		includeSubject:  false,
		resourceName:    "",
		messagingSystem: "",
	}
}

// WithSubject enables inclusion of the event subject in the span tags.
// Note: Event subjects may contain sensitive data, so this is opt-in.
func WithSubject() Option {
	return func(c *config) {
		c.includeSubject = true
	}
}

// WithResourceName sets the resource name for the span.
// If not provided, a default resource name will be used.
func WithResourceName(name string) Option {
	return func(c *config) {
		c.resourceName = name
	}
}

// WithMessagingSystem sets the messaging system tag for the span.
// Example:
// WithMessagingSystem(ext.MessagingSystemKafka)
// WithMessagingSystem(ext.MessagingSystemPubSub)
func WithMessagingSystem(system string) Option {
	return func(c *config) {
		c.messagingSystem = system
	}
}
