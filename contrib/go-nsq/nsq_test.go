// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2021 Datadog, Inc.
// Author: DataDog (https://github.com/DataDog/)

package nsq

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
