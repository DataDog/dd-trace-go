#!/bin/bash

orchestrion go test -shuffle=on ./... -coverprofile=coverage.txt -covermode=atomic -timeout 30m