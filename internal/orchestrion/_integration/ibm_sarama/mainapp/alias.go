// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package main

// Imported under another name than sarama, which the rules must handle the way
// orchestrion does.
import (
	kafka "github.com/IBM/sarama"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/ibm_sarama/internal/kafkamock"
)

// runAliased also passes a nil config, which sarama replaces with its own.
func runAliased(addrs []string, cfg *kafka.Config) error {
	sp, err := kafka.NewSyncProducer(addrs, nil)
	if err != nil {
		return err
	}
	if err := kafkamock.SendSync(sp); err != nil {
		return err
	}

	client, err := kafka.NewClient(addrs, cfg)
	if err != nil {
		return err
	}
	defer client.Close()

	c, err := kafka.NewConsumerFromClient(client)
	if err != nil {
		return err
	}
	return kafkamock.Consume(c)
}
