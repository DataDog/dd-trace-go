package memcache

import (
	"fmt"
	"testing"

	dnstrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/miekg/dns"

	"github.com/miekg/dns"
	"github.com/stretchr/testify/assert"
)

type Integration struct {
	msg *dns.Msg
	mux *dns.ServeMux
	//cl       *dnstrace.Client
	numSpans int
}

func New() *Integration {
	return &Integration{}
}

func (i *Integration) Name() string {
	return "contrib/miekg/dns"
}

func (i *Integration) Init(_ *testing.T) {
	i.mux = dns.NewServeMux()
	i.mux.HandleFunc(".", func(w dns.ResponseWriter, r *dns.Msg) {
		m := new(dns.Msg)
		m.SetReply(r)
		w.WriteMsg(m)
	})

	// calling dnstrace.ListenAndServe will call dns.ListenAndServe but all
	// requests will be traced
	dnstrace.ListenAndServe(":dns", "udp", i.mux)
}

func (i *Integration) GenSpans(t *testing.T) {
	i.msg = newMessage()

	// calling dnstrace.Exchange will call dns.Exchange but trace the request
	reply, err := dnstrace.Exchange(i.msg, "127.0.0.1:53")
	fmt.Println(reply, err)
	assert.NoError(t, err)

	// i.cl = newTracedClient()

	// _, _, e := i.cl.ExchangeContext(context.Background(), i.msg, i.mux.Addr)
	// assert.NoError(t, e)

	i.numSpans++
}

func (i *Integration) NumSpans() int {
	return i.numSpans
}

func newMessage() *dns.Msg {
	m := new(dns.Msg)
	m.SetQuestion("miek.nl.", dns.TypeMX)
	return m
}

// func newTracedClient() *dnstrace.Client {
// 	return &dnstrace.Client{Client: &dns.Client{Net: "udp"}}
// }
