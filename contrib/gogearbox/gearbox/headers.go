// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.
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
