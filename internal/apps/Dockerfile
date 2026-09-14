FROM golang:1.27.1@sha256:512690a5660563b57d37ecc31129e7f136e831db2aed24a1dbeb8ad7380dc0fa
COPY . /dd-trace-go
WORKDIR /dd-trace-go/internal/apps
# -t will download all dependencies, including test dependencies
RUN go get -t ./...
ENTRYPOINT [ "./run-scenario.bash" ]