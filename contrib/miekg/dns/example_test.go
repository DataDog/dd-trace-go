// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package dns_test

import (
	"fmt"

	"github.com/miekg/dns"

	dnstrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/miekg/dns"
)

func Example_client() {
	m := new(dns.Msg)
	m.SetQuestion("miek.nl.", dns.TypeMX)
	// calling dnstrace.Exchange will call dns.Exchange but trace the request
	reply, err := dnstrace.Exchange(m, "127.0.0.1:53")
	fmt.Println(reply, err)
}

func Example_server() {
	mux := dns.NewServeMux()
	mux.HandleFunc(".", func(w dns.ResponseWriter, r *dns.Msg) {
		m := new(dns.Msg)
		m.SetReply(r)
		w.WriteMsg(m)
	})
	// calling dnstrace.ListenAndServe will call dns.ListenAndServe but all
	// requests will be traced
	dnstrace.ListenAndServe(":dns", "udp", mux)
}
