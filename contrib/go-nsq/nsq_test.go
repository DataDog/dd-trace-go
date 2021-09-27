// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2021 Datadog, Inc.
// Author: CodapeWild (https://github.com/CodapeWild/)

package nsq

var (
	lookupdHTTPAddr = "127.0.0.1:4161"
	nsqdTCPAddr     = "127.0.0.1:4150"
	nsqdHTTPAddr    = "127.0.0.1:4151"
	topic           = "nsq_ddtrace_test"
	channel         = "nsq_ddtrace_test_channel_Jacky"
	msgBody         = []byte(`{"service":"nsq_ddtrace"}`)
	multiMsgBody    = [][]byte{msgBody, msgBody, msgBody}
)
