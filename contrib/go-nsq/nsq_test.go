// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2021 Datadog, Inc.
// Author: CodapeWild (https://github.com/CodapeWild/)

package nsq

import (
	"context"
	"log"
	"math/rand"
	"testing"
	"time"

	"github.com/nsqio/go-nsq"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
)

var (
	service         = "go-nsq"
	lookupdHttpAddr = "127.0.0.1:4161"
	nsqdTcpAddr     = "127.0.0.1:4150"
	nsqdHttpAddr    = "127.0.0.1:4151"
	topic           = "nsq_ddtrace_test"
	channels        = []string{"Jacky", "Caroline"}
	msgBody         = []byte(`{"service":"nsq_ddtrace"}`)
	multiMsgBody    = [][]byte{msgBody, msgBody, msgBody}
)

var (
	prodc  *Producer
	consus []*Consumer
)

func startProducer(t *testing.T) {
	var err error
	prodc, err = NewProducer(nsqdTcpAddr, nsq.NewConfig(), WithService(service), WithContext(context.Background()))
	if err != nil {
		t.Error(err)
		t.Fail()
	}

	for i := 0; i < 3; i++ {
		go func() {
			var err error
			for {
				bts := make([]byte, 100)
				rand.Read(bts)
				if err = prodc.Publish(topic, bts); err != nil {
					t.Error(err)
					t.Fail()
				}
				var btss [][]byte
				for j := 0; j < 10; j++ {
					btss = append(btss, bts)
					rand.Read(bts)
				}
				if err = prodc.MultiPublish(topic, btss); err != nil {
					t.Error(err)
					t.Fail()
				}
				if err = prodc.DeferredPublish(topic, time.Second, bts); err != nil {
					t.Error(err)
					t.Fail()
				}
				time.Sleep(time.Duration(rand.Intn(10)+1) * time.Second)
			}
		}()
	}
}

func msgHandler(msg *nsq.Message) error {
	log.Println(string(msg.Body))

	return nil
}

func startConsumer(t *testing.T) {
	for _, channel := range channels {
		for i := 0; i < 3; i++ {
			go func(channel string) {
				consu, err := NewConsumer(topic, channel, nsq.NewConfig(), WithService(service), WithContext(context.Background()))
				if err != nil {
					t.Error(err)
					t.Fail()
				}
				consu.AddHandler(nsq.HandlerFunc(msgHandler))
				consu.ConnectToNSQLookupd(lookupdHttpAddr)
				consus = append(consus, consu)
			}(channel)
		}
	}
}

func TestNSQTracing(t *testing.T) {
	mctr := mocktracer.Start()
	defer mctr.Stop()

	startProducer(t)
	time.Sleep(3 * time.Second)
	startConsumer(t)
	time.Sleep(10 * time.Second)

	if prodc != nil {
		prodc.Stop()
	}
	for _, consu := range consus {
		consu.Stop()
	}

	spans := mctr.FinishedSpans()
	for _, span := range spans {
		t.Log(span.String())
	}
}
