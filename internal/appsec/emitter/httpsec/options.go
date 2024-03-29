package httpsec

import "net/http"

type WrapHandlerCfg struct {
	OnBlock            []func()
	ResponseHdrFetcher func(http.ResponseWriter) http.Header
}
type WrapHandlerOption func(*WrapHandlerCfg)

func defaultWrapHandlerCfg() *WrapHandlerCfg {
	return &WrapHandlerCfg{
		OnBlock: []func(){},
		ResponseHdrFetcher: func(w http.ResponseWriter) http.Header {
			return w.Header()
		},
	}
}

func WithOnBlock(f ...func()) WrapHandlerOption {
	return func(cfg *WrapHandlerCfg) {
		cfg.OnBlock = append(cfg.OnBlock, f...)
	}
}
