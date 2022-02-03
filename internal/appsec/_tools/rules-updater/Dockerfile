FROM tdewolff/minify as minify
ARG version
ADD https://raw.githubusercontent.com/DataDog/appsec-event-rules/$version/v2/build/recommended.json /home
RUN minify --type=json -o /home/out.json /home/recommended.json

FROM scratch
ARG version=1.2.5
COPY --from=minify /home/out.json rules-$version-min.json
