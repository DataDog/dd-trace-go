// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

// worker-pool-bottleneck implements a http service that demonstrates a worker
// pool bottleneck. In particular the service simulates an application that
// has a queue processing pipeline that consists of:
//
// 1. ConsumeMessageWorker: Pulls messages from a queue.
// 2. DecodeMessageWorker: Decodes messages.
// 3. LLMMessageWorker: Makes a long-latency call.
// 4. PublishMessageWorker: Publishes messages.
//
// The LLMMessageWorker is the bottleneck in the pipeline because it doesn't
// have enough workers to keep up with the other workers. This causes the
// ConsumeMessageWorker and DecodeMessageWorker to block on send operations.
//
// The primary use case is to take screenshots of the timeline feature.
package main

import (
	"encoding/json"
	"io"
	"log"
	"math/rand/v2"
	"net"
	"net/http"
	"time"

	httptrace "github.com/DataDog/dd-trace-go/contrib/net/http/v2"
	"github.com/DataDog/dd-trace-go/internal/apps/v2"
)

func main() {
	// Init queue
	queue, err := NewQueue()
	if err != nil {
		log.Fatalf("failed to create queue: %s", err.Error())
	}

	// Start app
	app := apps.Config{}
	app.RunHTTP(func() http.Handler {
		// Setup workers
		consumeDecode := make(chan []byte)
		decodeLLM := make(chan any)
		llmPublish := make(chan any)
		go ConsumeMessageWorker(queue, consumeDecode)
		for range 4 {
			go DecodeMessageWorker(consumeDecode, decodeLLM)
			go LLMMessageWorker(decodeLLM, llmPublish, app.HTTPAddr())
			go PublishMessageWorker(llmPublish)
		}

		// Setup HTTP handlers
		mux := httptrace.NewServeMux()
		mux.HandleFunc("/queue/push", QueuePushHandler(queue))
		mux.HandleFunc("/llm", LLMHandler())
		return mux
	})
}

func QueuePushHandler(queue *Queue) http.HandlerFunc {
	data, _ := fakePayload(16 * 1024)
	return func(w http.ResponseWriter, r *http.Request) {
		for i := 0; i < 100; i++ {
			if err := queue.Push(data); err != nil {
				log.Fatalf("failed to push message: %s", err.Error())
			}
		}
	}
}

func LLMHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Flush out the headers and a short message
		w.WriteHeader(http.StatusOK)
		rc := http.NewResponseController(w)
		w.Write([]byte("hello\n"))
		rc.Flush()
		// Wait to simulate a long time to respond
		time.Sleep(time.Duration(rand.Float64() * 100 * float64(time.Millisecond)))
		// Flush out another short message and finish the response
		w.Write([]byte("world\n"))
		rc.Flush()
	}
}

func fakePayload(elements int) ([]byte, error) {
	var payload []int
	for i := 0; i < elements; i++ {
		payload = append(payload, i)
	}
	return json.Marshal(payload)
}

func ConsumeMessageWorker(queue *Queue, decode chan<- []byte) {
	for {
		msg, err := queue.Pull()
		if err != nil {
			log.Fatalf("failed to pull message: %s", err.Error())
		}
		decode <- msg
	}
}

func DecodeMessageWorker(decode <-chan []byte, llm chan<- any) {
	for {
		msg := <-decode
		var data interface{}
		if err := json.Unmarshal(msg, &data); err != nil {
			log.Fatalf("failed to decode message: %v: %q", err, string(msg))
		}
		llm <- data
	}
}

func LLMMessageWorker(llm <-chan any, db chan<- any, addr net.Addr) {
	for {
		msg := <-llm
		llmCall(addr)
		db <- msg
	}
}

func PublishMessageWorker(db <-chan any) {
	for {
		<-db
	}
}

func llmCall(addr net.Addr) error {
	res, err := http.Get("http://" + addr.String() + "/llm")
	if err != nil {
		return err
	}
	defer res.Body.Close()
	// Ensure that llmCall will spend most of its time in a networking state
	// so it looks purple in the timeline.
	_, err = io.ReadAll(res.Body)
	return err
}
