#!/bin/bash

orchestrion go test ../_integration/... -shuffle=on -coverprofile=coverage.txt -covermode=atomic -timeout=30m