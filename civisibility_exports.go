// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

//go:build civisibility
// +build civisibility

// go build -tags civisibility -buildmode=c-shared -o libcivisibility.dylib civisibility_exports.go

package main

import "C"
import (
	civisibility "gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/integrations"
	"sync"
)

var session civisibility.TestSession
var modulesMutex sync.RWMutex
var modules = make(map[uint64]civisibility.TestModule)

//export civisibility_initialize
func civisibility_initialize(framework *C.char, frameworkVersion *C.char) {
	civisibility.EnsureCiVisibilityInitialization()
	if framework == nil || frameworkVersion == nil {
		session = civisibility.CreateTestSession()
	} else {
		session = civisibility.CreateTestSession(civisibility.WithTestSessionFramework(C.GoString(framework), C.GoString(frameworkVersion)))
	}
}

//export civisibility_shutdown
func civisibility_shutdown(exitCode C.int) {
	if session != nil {
		session.Close(int(exitCode))
	}
	civisibility.ExitCiVisibility()
}

//export civisibility_create_module
func civisibility_create_module(name *C.char, framework *C.char, frameworkVersion *C.char) C.ulonglong {
	modulesMutex.Lock()
	defer modulesMutex.Unlock()
	module := session.GetOrCreateModule(C.GoString(name), civisibility.WithTestModuleFramework(C.GoString(framework), C.GoString(frameworkVersion)))
	modules[module.ModuleID()] = module
	return C.ulonglong(module.ModuleID())
}

//export civisibility_close_module
func civisibility_close_module(module_id C.ulonglong) {
	modulesMutex.Lock()
	defer modulesMutex.Unlock()
	moduleID := uint64(module_id)
	if module, ok := modules[moduleID]; ok {
		module.Close()
		delete(modules, moduleID)
	}
}

func main() {}
