// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Command mainapp builds sarama producers and consumers from package main,
// which does not import the contrib, and handles one message with each.
// TestConstructorsInMain builds and runs it.
package main

import (
	"fmt"
	"os"

	"github.com/Shopify/sarama"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/shopify_sarama/internal/kafkamock"
)

// reporter lets the mock broker report failures outside a test.
type reporter struct{}

func (reporter) Error(args ...any)                 { fmt.Fprintln(os.Stderr, args...) }
func (reporter) Errorf(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...) }
func (r reporter) Fatal(args ...any)               { r.Error(args...); os.Exit(1) }
func (r reporter) Fatalf(format string, args ...any) {
	r.Errorf(format, args...)
	os.Exit(1)
}
func (reporter) Helper() {}

func main() {
	broker := kafkamock.Start(reporter{})
	defer broker.Close()
	if err := run([]string{broker.Addr()}, kafkamock.Config()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	// The injected `defer tracer.Stop()` flushes the spans, so main has to
	// return normally.
	fmt.Println("mainapp: done")
}

func run(addrs []string, cfg *sarama.Config) error {
	sp, err := sarama.NewSyncProducer(addrs, cfg)
	if err != nil {
		return err
	}
	if err := kafkamock.SendSync(sp); err != nil {
		return err
	}

	ap, err := sarama.NewAsyncProducer(addrs, cfg)
	if err != nil {
		return err
	}
	if err := kafkamock.SendAsync(ap); err != nil {
		return err
	}

	c, err := sarama.NewConsumer(addrs, cfg)
	if err != nil {
		return err
	}
	if err := kafkamock.Consume(c); err != nil {
		return err
	}

	client, err := sarama.NewClient(addrs, cfg)
	if err != nil {
		return err
	}
	defer client.Close()

	sp, err = sarama.NewSyncProducerFromClient(client)
	if err != nil {
		return err
	}
	if err := kafkamock.SendSync(sp); err != nil {
		return err
	}

	ap, err = sarama.NewAsyncProducerFromClient(client)
	if err != nil {
		return err
	}
	if err := kafkamock.SendAsync(ap); err != nil {
		return err
	}
	return runAliased(addrs, cfg)
}
