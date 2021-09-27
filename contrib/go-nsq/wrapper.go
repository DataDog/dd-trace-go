// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2021 Datadog, Inc.
// Author: CodapeWild (https://github.com/CodapeWild/)

package nsq

import "github.com/nsqio/go-nsq"

func HandlerFuncWrapper(topic, channel string, handler nsq.HandlerFunc) nsq.HandlerFunc {
	return nil
}
