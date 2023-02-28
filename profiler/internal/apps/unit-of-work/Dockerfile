FROM golang:latest
ADD . /dd-trace-go
WORKDIR /dd-trace-go/profiler/internal/apps/unit-of-work
RUN go get -t
RUN go install
CMD ./run.bash
