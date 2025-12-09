// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package gotesting

import (
	"reflect"
	"testing"
)

type MySuite struct {
	T *testing.T
}

func Run(t *testing.T, suite *MySuite) {
	suite.T = t

	tests := []testing.InternalTest{}
	methodFinder := reflect.TypeOf(suite)
	for i := 0; i < methodFinder.NumMethod(); i++ {
		method := methodFinder.Method(i)

		parentT := t
		test := testing.InternalTest{
			Name: method.Name,
			F: func(t *testing.T) {
				suite.T = t
				defer func() {
					suite.T = parentT
				}()
				method.Func.Call([]reflect.Value{reflect.ValueOf(suite)})
			},
		}
		tests = append(tests, test)
	}

	for _, test := range tests {
		(*T)(t).Run(test.Name, test.F)
	}
}
