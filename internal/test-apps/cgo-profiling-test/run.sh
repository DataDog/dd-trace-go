#!/bin/bash
# http://redsymbol.net/articles/unofficial-bash-strict-mode/
set -euo pipefail
IFS=$'\n\t'

go install github.com/tsenart/vegeta@latest

go build
./cgo-profiling-test &
sleep 1 # wait for startup

# Generate some load
$(go env GOPATH)/bin/vegeta attack -connections 10 -rate 1000 -output vegeta.bin << EOF 
POST http://localhost:8765/update?name=foo&color=blue
POST http://localhost:8765/update?name=bar&color=green
POST http://localhost:8765/update?name=baz&color=purple
POST http://localhost:8765/update?name=foo&color=yello
POST http://localhost:8765/update?name=bar&color=red
POST http://localhost:8765/update?name=baz&color=gray
GET http://localhost:8765/get?name=foo
GET http://localhost:8765/get?name=bar
GET http://localhost:8765/get?name=baz
GET http://localhost:8765/get?name=foo
GET http://localhost:8765/get?name=bar
GET http://localhost:8765/get?name=baz
GET http://localhost:8765/get?name=foo
GET http://localhost:8765/get?name=bar
GET http://localhost:8765/get?name=baz
GET http://localhost:8765/get?name=foo
GET http://localhost:8765/get?name=bar
GET http://localhost:8765/get?name=baz
EOF

