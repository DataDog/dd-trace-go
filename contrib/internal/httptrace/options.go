package httptrace

import "os"

type config struct {
	ipHeader string
}

var cfg *config

func newConfig() *config {
	return &config{
		ipHeader: os.Getenv("DD_APPSEC_IPHEADER"),
	}
}

func init() {
	cfg = newConfig()
}
