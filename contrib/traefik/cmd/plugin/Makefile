WASM_OUT=plugin.wasm

build:
	GOOS=wasip1 GOARCH=wasm CGO_ENABLED=0 go build -buildmode=c-shared -trimpath -o $(WASM_OUT) main.go helper.go extproc.go