FROM golang:1.27.4@sha256:205c7636d74e0ffbe3d007162df7d74f859f5a8c8322dfb9f907977686c126cf
COPY . /dd-trace-go
WORKDIR /dd-trace-go/internal/apps
# -t will download all dependencies, including test dependencies
RUN go get -t ./...
ENTRYPOINT [ "./run-scenario.bash" ]