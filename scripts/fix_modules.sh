#!/bin/bash

# This scripts runs go mod tidy on all the go modules of the repo, and additionally it adds missing replace directives
# for local imports.

go run ./tools/fixmodules -root=. .
