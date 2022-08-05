package gearbox

import (
	"github.com/gogearbox/gearbox"
)

type gearboxContextCarrier struct {
	ctx gearbox.Context
}

func (gcc gearboxContextCarrier) ForeachKey(handler func(key, val string) error) error {
	errorList := []error{}
	gcc.ctx.Context().Request.Header.VisitAll(func(key, value []byte) {
		k, v := string(key), string(value)
		err := handler(k, v)
		if err != nil {
			errorList = append(errorList, err)
		}
	})

	if len(errorList) > 0 {
		return errorList[0]
	}
	return nil
}
