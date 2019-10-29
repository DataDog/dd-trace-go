// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2019 Datadog, Inc.

package twirp_test

import (
	"context"
	"fmt"
	"net/http"

	twirptrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/twitchtv/twirp"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"github.com/twitchtv/twirp/example"
)

func ExampleWrapClient() {
	tracer.Start()
	defer tracer.Stop()

	client := example.NewHaberdasherJSONClient("http://localhost:8080", twirptrace.WrapClient(&http.Client{}))
	for i := 0; i < 10; i++ {
		hat, err := client.MakeHat(context.Background(), &example.Size{Inches: 6})
		if err != nil {
			fmt.Println("error making hat:", err)
			continue
		}
		fmt.Println("made hat:", hat)
	}
}

type hatmaker struct{}

func (hatmaker) MakeHat(ctx context.Context, size *example.Size) (*example.Hat, error) {
	return &example.Hat{
		Size:  42,
		Color: "cornflower blue",
		Name:  "oversized blue hat",
	}, nil
}

func ExampleWrapServer() {
	tracer.Start()
	defer tracer.Stop()

	server := example.NewHaberdasherServer(hatmaker{}, twirptrace.NewServerHooks())
	traced := twirptrace.WrapServer(server)
	http.ListenAndServe(":8080", traced)
}
