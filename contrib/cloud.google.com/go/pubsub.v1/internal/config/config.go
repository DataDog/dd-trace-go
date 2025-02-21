// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package config

import "github.com/DataDog/dd-trace-go/v2/instrumentation"

var instr *instrumentation.Instrumentation

func init() {
	instr = instrumentation.Load(instrumentation.PackageGCPPubsub)
}

type Config struct {
	ServiceName     string
	PublishSpanName string
	ReceiveSpanName string
	Measured        bool
}

// Option describes options for the Pub/Sub integration.
type Option interface {
	Apply(*Config)
}

func Default() *Config {
	return &Config{
		ServiceName:     instr.ServiceName(instrumentation.ComponentConsumer, nil),
		PublishSpanName: instr.OperationName(instrumentation.ComponentProducer, nil),
		ReceiveSpanName: instr.OperationName(instrumentation.ComponentConsumer, nil),
		Measured:        false,
	}
}

func Logger() instrumentation.Logger {
	return instr.Logger()
}
