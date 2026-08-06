FROM golang:1.26.5@sha256:2005724102f45917a63e9d092fc0e4ea56ea575048ce147caad5f5f61502c365
COPY . /dd-trace-go
WORKDIR /dd-trace-go/internal/apps
# -t will download all dependencies, including test dependencies
RUN go get -t ./...
ENTRYPOINT [ "./run-scenario.bash" ]