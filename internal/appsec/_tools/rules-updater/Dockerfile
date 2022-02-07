FROM tdewolff/minify as minify
ARG version
ADD https://raw.githubusercontent.com/DataDog/appsec-event-rules/$version/v2/build/recommended.json /home
RUN minify --type=json -o /home/out.json /home/recommended.json

FROM golang as go-format
ARG version
ENV VERSION=$version
COPY escaper/ /home/
COPY --from=minify /home/out.json /home/rules.json
WORKDIR /home/
RUN go run escaper.go ${VERSION} > rule.go

FROM scratch
ARG version=1.2.5
COPY --from=go-format /home/rule.go rule.go
