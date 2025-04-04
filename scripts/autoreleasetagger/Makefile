.PHONY: fmt
fmt:
	go run mvdan.cc/gofumpt@latest -l -w .

.PHONY: vet
vet:
	GOWORK=off go vet ./...

.PHONY: fix
fix: build
	GOWORK=off golangci-lint run --fix --enable-all --no-config ./...

.PHONY: lint
lint:
	GOWORK=off golangci-lint run --enable-all --no-config ./...

.PHONY: test
test: deps
	GOWORK=off go test -v ./...

deps:
	go mod tidy

.PHONY: build
build: deps
	go build -o autoreleasetagger ./main.go

tmp/help.txt: build
	./autoreleasetagger -h > tmp/help.txt 2>&1

.PHONY: README.md
README.md: tmp/help.txt
	go run github.com/campoy/embedmd@latest -w README.md
