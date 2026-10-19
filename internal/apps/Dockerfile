FROM golang:1.27.2@sha256:820153096fda4de6ac1b7194d12e416b7dd8f4e9a33d17e0345b111b945162d8
COPY . /dd-trace-go
WORKDIR /dd-trace-go/internal/apps
# -t will download all dependencies, including test dependencies
RUN go get -t ./...
ENTRYPOINT [ "./run-scenario.bash" ]