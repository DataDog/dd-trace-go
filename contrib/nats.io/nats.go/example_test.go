// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2021 Datadog, Inc.

package nats

import (
	"context"
	"fmt"

	natstrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/nats.io/nats.go"

	"github.com/nats-io/nats.go"
)

func Example() {
	// Open a regular connection onto your NATS server
	nc, err := nats.Connect("nats://127.0.0.1:4222")
	if err != nil {
		panic(err)
	}

	// Wrap it
	wnc := natstrace.WrapConn(nc)

	// Subscribe to a topic
	s, err := wnc.SubscribeSync("mytopic")
	if err != nil {
		panic(err)
	}
	defer s.Drain()

	// Echo messages published in the subscribed topic
	go func() {
		for {
			msgs, err := s.Fetch(context.TODO(), 1)
			if err != nil {
				panic(err)
			}

			for _, msg := range msgs {
				fmt.Printf("received message %v", msg.Data)
			}
		}
	}()

	// Publish a message to the topic
	err = wnc.PublishMsg(context.TODO(), &nats.Msg{
		Subject: "mytopic",
		Data:    []byte("coucou"),
	})
	if err != nil {
		panic(err)
	}
}
