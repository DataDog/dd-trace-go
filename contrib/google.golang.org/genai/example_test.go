// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package genaitrace_test

import (
	"context"

	"google.golang.org/genai"

	genaitrace "github.com/DataDog/dd-trace-go/contrib/google.golang.org/genai/v2"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

func Example() {
	tracer.Start()
	defer tracer.Stop()

	raw, err := genai.NewClient(context.Background(), &genai.ClientConfig{
		APIKey:  "your-api-key",
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		panic(err)
	}
	client := genaitrace.WrapClient(raw)

	resp, err := client.Models.GenerateContent(
		context.Background(),
		"gemini-2.0-flash",
		[]*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "Hello!"}}}},
		nil,
	)
	_ = resp
	_ = err
}

func Example_chat() {
	tracer.Start()
	defer tracer.Stop()

	raw, err := genai.NewClient(context.Background(), &genai.ClientConfig{
		APIKey:  "your-api-key",
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		panic(err)
	}
	client := genaitrace.WrapClient(raw)

	chat, err := client.Chats.Create(context.Background(), "gemini-2.0-flash", nil, nil)
	if err != nil {
		panic(err)
	}
	resp, err := chat.SendMessage(context.Background(), genai.Part{Text: "Hello!"})
	_ = resp
	_ = err
}
