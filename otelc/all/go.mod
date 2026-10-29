module github.com/DataDog/dd-trace-go/otelc/all/v2

go 1.26.0

replace github.com/DataDog/dd-trace-go/v2 => ../..

replace github.com/DataDog/dd-trace-go/contrib/go-chi/chi/v2 => ../../contrib/go-chi/chi

replace github.com/DataDog/dd-trace-go/contrib/go-chi/chi.v5/v2 => ../../contrib/go-chi/chi.v5

require (
	github.com/DataDog/dd-trace-go/contrib/aws/aws-sdk-go/otelc/v2 v2.13.0-dev
	github.com/DataDog/dd-trace-go/contrib/database/sql/otelc/v2 v2.13.0-dev
	github.com/DataDog/dd-trace-go/contrib/gin-gonic/gin/otelc/v2 v2.0.0-00010101000000-000000000000
	github.com/DataDog/dd-trace-go/contrib/go-chi/chi.v5/otelc/v2 v2.13.0-dev
	github.com/DataDog/dd-trace-go/contrib/go-chi/chi/otelc/v2 v2.13.0-dev
	github.com/DataDog/dd-trace-go/contrib/go.mongodb.org/mongo-driver.v2/otelc/v2 v2.13.0-dev
	github.com/DataDog/dd-trace-go/contrib/go.mongodb.org/mongo-driver/otelc/v2 v2.13.0-dev
	github.com/DataDog/dd-trace-go/contrib/gofiber/fiber.v2/otelc/v2 v2.13.0-dev
	github.com/DataDog/dd-trace-go/contrib/gomodule/redigo/otelc/v2 v2.13.0-dev
	github.com/DataDog/dd-trace-go/contrib/gorilla/mux/otelc/v2 v2.13.0-dev
	github.com/DataDog/dd-trace-go/contrib/net/http/otelc/v2 v2.13.0-dev
	github.com/DataDog/dd-trace-go/v2 v2.13.0-dev
)

require (
	github.com/99designs/gqlgen v0.17.97 // indirect
	github.com/DataDog/dd-trace-go/contrib/99designs/gqlgen/v2 v2.13.0-dev // indirect
	github.com/DataDog/dd-trace-go/contrib/aws/aws-sdk-go/v2 v2.13.0-dev // indirect
	github.com/DataDog/dd-trace-go/contrib/gin-gonic/gin/v2 v2.13.0-dev // indirect
	github.com/DataDog/dd-trace-go/contrib/go.mongodb.org/mongo-driver.v2/v2 v2.13.0-dev // indirect
	github.com/DataDog/dd-trace-go/contrib/go.mongodb.org/mongo-driver/v2 v2.13.0-dev // indirect
	github.com/DataDog/dd-trace-go/contrib/gofiber/fiber.v2/v2 v2.13.0-dev // indirect
	github.com/DataDog/dd-trace-go/contrib/google.golang.org/grpc/v2 v2.13.0-dev // indirect
	github.com/DataDog/dd-trace-go/contrib/gorilla/mux/v2 v2.13.0-dev // indirect
	github.com/DataDog/dd-trace-go/contrib/julienschmidt/httprouter/v2 v2.13.0-dev // indirect
	github.com/DataDog/dd-trace-go/contrib/labstack/echo.v4/v2 v2.13.0-dev // indirect
	github.com/DataDog/dd-trace-go/contrib/labstack/echo.v5/v2 v2.13.0-dev // indirect
	github.com/DataDog/dd-trace-go/contrib/valyala/fasthttp/v2 v2.13.0-dev // indirect
	github.com/aws/aws-sdk-go v1.55.5 // indirect
	github.com/bytedance/sonic v1.12.6 // indirect
	github.com/bytedance/sonic/loader v0.2.1 // indirect
	github.com/clipperhouse/uax29/v2 v2.7.0 // indirect
	github.com/cloudwego/base64x v0.1.4 // indirect
	github.com/cloudwego/iasm v0.2.0 // indirect
	github.com/gabriel-vasile/mimetype v1.4.8 // indirect
	github.com/gin-contrib/sse v1.0.0 // indirect
	github.com/gin-gonic/gin v1.10.1 // indirect
	github.com/go-playground/locales v0.14.1 // indirect
	github.com/go-playground/universal-translator v0.18.1 // indirect
	github.com/go-playground/validator/v10 v10.23.0 // indirect
	github.com/goccy/go-json v0.10.4 // indirect
	github.com/gofiber/fiber/v2 v2.52.13 // indirect
	github.com/gorilla/mux v1.8.1 // indirect
	github.com/jmespath/go-jmespath v0.4.0 // indirect
	github.com/julienschmidt/httprouter v1.3.0 // indirect
	github.com/labstack/echo/v4 v4.15.3 // indirect
	github.com/labstack/echo/v5 v5.2.0 // indirect
	github.com/labstack/gommon v0.5.0 // indirect
	github.com/leodido/go-urn v1.4.0 // indirect
	github.com/mattn/go-colorable v0.1.16 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/mattn/go-runewidth v0.0.21 // indirect
	github.com/molecule-man/go-brrr v1.1.1 // indirect
	github.com/pelletier/go-toml/v2 v2.2.3 // indirect
	github.com/sosodev/duration v1.4.0 // indirect
	github.com/twitchyliquid64/golang-asm v0.15.1 // indirect
	github.com/ugorji/go/codec v1.2.12 // indirect
	github.com/valyala/bytebufferpool v1.0.0 // indirect
	github.com/valyala/fasthttp v1.75.0 // indirect
	github.com/valyala/fasttemplate v1.2.2 // indirect
	github.com/vektah/gqlparser/v2 v2.5.63 // indirect
	go.mongodb.org/mongo-driver v1.17.7 // indirect
	go.mongodb.org/mongo-driver/v2 v2.4.2 // indirect
	golang.org/x/arch v0.8.0 // indirect
	golang.org/x/crypto v0.58.0 // indirect
	golang.org/x/net v0.61.0 // indirect
	golang.org/x/sync v0.24.0 // indirect
	golang.org/x/text v0.43.0 // indirect
	google.golang.org/grpc v1.83.2 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

require (
	github.com/DataDog/datadog-agent/comp/core/tagger/origindetection v0.82.0 // indirect
	github.com/DataDog/datadog-agent/pkg/obfuscate v0.82.0 // indirect
	github.com/DataDog/datadog-agent/pkg/opentelemetry-mapping-go/otlp/attributes v0.82.0 // indirect
	github.com/DataDog/datadog-agent/pkg/proto v0.82.0 // indirect
	github.com/DataDog/datadog-agent/pkg/remoteconfig/state v0.82.0 // indirect
	github.com/DataDog/datadog-agent/pkg/trace v0.82.0 // indirect
	github.com/DataDog/datadog-agent/pkg/trace/log v0.82.0 // indirect
	github.com/DataDog/datadog-agent/pkg/trace/stats v0.82.0 // indirect
	github.com/DataDog/datadog-agent/pkg/trace/traceutil v0.82.0 // indirect
	github.com/DataDog/datadog-go/v5 v5.9.0 // indirect
	github.com/DataDog/dd-trace-go/contrib/99designs/gqlgen/otelc/v2 v2.13.0-dev
	github.com/DataDog/dd-trace-go/contrib/database/sql/v2 v2.13.0-dev // indirect
	github.com/DataDog/dd-trace-go/contrib/go-chi/chi.v5/v2 v2.13.0-dev // indirect
	github.com/DataDog/dd-trace-go/contrib/go-chi/chi/v2 v2.13.0-dev // indirect
	github.com/DataDog/dd-trace-go/contrib/gomodule/redigo/v2 v2.13.0-dev // indirect
	github.com/DataDog/dd-trace-go/contrib/google.golang.org/grpc/otelc/v2 v2.13.0-dev
	github.com/DataDog/dd-trace-go/contrib/labstack/echo.v4/otelc/v2 v2.13.0-dev
	github.com/DataDog/dd-trace-go/contrib/labstack/echo.v5/otelc/v2 v2.13.0-dev
	github.com/DataDog/dd-trace-go/contrib/net/http/v2 v2.13.0-dev // indirect
	github.com/DataDog/go-libddwaf/v5 v5.0.0 // indirect
	github.com/DataDog/go-runtime-metrics-internal v0.0.4-0.20260217080614-b0f4edc38a6d // indirect
	github.com/DataDog/go-sqllexer v0.2.3 // indirect
	github.com/DataDog/go-tuf v1.1.1-0.5.2 // indirect
	github.com/DataDog/sketches-go v1.4.8 // indirect
	github.com/Microsoft/go-winio v0.6.2 // indirect
	github.com/cenkalti/backoff/v5 v5.0.3 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/cihub/seelog v0.0.0-20170130134532-f561c5e57575 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/ebitengine/purego v0.10.0 // indirect
	github.com/go-chi/chi v1.5.5 // indirect
	github.com/go-chi/chi/v5 v5.3.0 // indirect
	github.com/go-ole/go-ole v1.3.0 // indirect
	github.com/gomodule/redigo v1.9.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/hashicorp/go-version v1.9.0 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/klauspost/compress v1.20.1 // indirect
	github.com/klauspost/cpuid/v2 v2.3.0 // indirect
	github.com/linkdata/deadlock v0.5.5 // indirect
	github.com/lufia/plan9stats v0.0.0-20260330125221-c963978e514e // indirect
	github.com/minio/simdjson-go v0.4.5 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.3-0.20250322232337-35a7c28c31ee // indirect
	github.com/outcaste-io/ristretto v0.2.3 // indirect
	github.com/petermattis/goid v0.0.0-20260226131333-17d1149c6ac6 // indirect
	github.com/philhofer/fwd v1.2.0 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/planetscale/vtprotobuf v0.6.1-0.20240319094008-0393e58bdf10 // indirect
	github.com/power-devops/perfstat v0.0.0-20240221224432-82ca36839d55 // indirect
	github.com/puzpuzpuz/xsync/v4 v4.5.0 // indirect
	github.com/secure-systems-lab/go-securesystemslib v0.11.0 // indirect
	github.com/shirou/gopsutil/v4 v4.26.6 // indirect
	github.com/tinylib/msgp v1.6.4 // indirect
	github.com/tklauser/go-sysconf v0.3.16 // indirect
	github.com/tklauser/numcpus v0.11.0 // indirect
	github.com/trailofbits/go-mutexasserts v0.0.0-20250514102930-c1f3d2e37561 // indirect
	github.com/yusufpapurcu/wmi v1.2.4 // indirect
	go.opentelemetry.io/collector/component v1.61.0 // indirect
	go.opentelemetry.io/collector/featuregate v1.61.0 // indirect
	go.opentelemetry.io/collector/pdata v1.61.0 // indirect
	go.opentelemetry.io/collector/pdata/pprofile v0.155.0 // indirect
	go.opentelemetry.io/otel v1.45.0 // indirect
	go.opentelemetry.io/otel/metric v1.45.0 // indirect
	go.opentelemetry.io/otel/trace v1.45.0 // indirect
	go.opentelemetry.io/otelc/pkg v0.0.0-20261015110818-d3d72efe9f25 // indirect
	go.opentelemetry.io/proto/otlp v1.11.0 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	go.uber.org/mock v0.6.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.uber.org/zap v1.28.0 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/exp v0.0.0-20260529124908-c761662dc8c9 // indirect
	golang.org/x/mod v0.42.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
	golang.org/x/time v0.15.0 // indirect
	golang.org/x/xerrors v0.0.0-20240903120638-7835f813f4da // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260803160001-6ac0973c030d // indirect
	google.golang.org/protobuf v1.36.12 // indirect
)

replace github.com/DataDog/dd-trace-go/contrib/go-chi/chi/otelc/v2 => ../../contrib/go-chi/chi/otelc

replace github.com/DataDog/dd-trace-go/contrib/go-chi/chi.v5/otelc/v2 => ../../contrib/go-chi/chi.v5/otelc

replace github.com/DataDog/dd-trace-go/contrib/net/http/otelc/v2 => ../../contrib/net/http/otelc

replace github.com/DataDog/dd-trace-go/contrib/net/http/v2 => ../../contrib/net/http

replace github.com/DataDog/dd-trace-go/contrib/database/sql/otelc/v2 => ../../contrib/database/sql/otelc

replace github.com/DataDog/dd-trace-go/contrib/database/sql/v2 => ../../contrib/database/sql

replace github.com/DataDog/dd-trace-go/contrib/gomodule/redigo/otelc/v2 => ../../contrib/gomodule/redigo/otelc

replace github.com/DataDog/dd-trace-go/contrib/gomodule/redigo/v2 => ../../contrib/gomodule/redigo

replace github.com/DataDog/dd-trace-go/contrib/google.golang.org/grpc/otelc/v2 => ../../contrib/google.golang.org/grpc/otelc

replace github.com/DataDog/dd-trace-go/contrib/google.golang.org/grpc/v2 => ../../contrib/google.golang.org/grpc

replace github.com/DataDog/dd-trace-go/instrumentation/testutils/grpc/v2 => ../../instrumentation/testutils/grpc

replace github.com/DataDog/dd-trace-go/contrib/99designs/gqlgen/otelc/v2 => ../../contrib/99designs/gqlgen/otelc

replace github.com/DataDog/dd-trace-go/contrib/99designs/gqlgen/v2 => ../../contrib/99designs/gqlgen

replace github.com/DataDog/dd-trace-go/contrib/go.mongodb.org/mongo-driver/otelc/v2 => ../../contrib/go.mongodb.org/mongo-driver/otelc

replace github.com/DataDog/dd-trace-go/contrib/go.mongodb.org/mongo-driver/v2 => ../../contrib/go.mongodb.org/mongo-driver

replace github.com/DataDog/dd-trace-go/contrib/go.mongodb.org/mongo-driver.v2/otelc/v2 => ../../contrib/go.mongodb.org/mongo-driver.v2/otelc

replace github.com/DataDog/dd-trace-go/contrib/go.mongodb.org/mongo-driver.v2/v2 => ../../contrib/go.mongodb.org/mongo-driver.v2

require github.com/DataDog/dd-trace-go/contrib/julienschmidt/httprouter/otelc/v2 v2.13.0-dev

replace github.com/DataDog/dd-trace-go/contrib/julienschmidt/httprouter/otelc/v2 => ../../contrib/julienschmidt/httprouter/otelc

replace github.com/DataDog/dd-trace-go/contrib/julienschmidt/httprouter/v2 => ../../contrib/julienschmidt/httprouter

replace github.com/DataDog/dd-trace-go/contrib/gorilla/mux/otelc/v2 => ../../contrib/gorilla/mux/otelc

replace github.com/DataDog/dd-trace-go/contrib/gorilla/mux/v2 => ../../contrib/gorilla/mux

replace github.com/DataDog/dd-trace-go/contrib/gin-gonic/gin/otelc/v2 => ../../contrib/gin-gonic/gin/otelc

replace github.com/DataDog/dd-trace-go/contrib/gin-gonic/gin/v2 => ../../contrib/gin-gonic/gin

replace github.com/DataDog/dd-trace-go/contrib/gofiber/fiber.v2/otelc/v2 => ../../contrib/gofiber/fiber.v2/otelc

replace github.com/DataDog/dd-trace-go/contrib/gofiber/fiber.v2/v2 => ../../contrib/gofiber/fiber.v2

require github.com/DataDog/dd-trace-go/contrib/valyala/fasthttp/otelc/v2 v2.13.0-dev

replace github.com/DataDog/dd-trace-go/contrib/valyala/fasthttp/otelc/v2 => ../../contrib/valyala/fasthttp/otelc

replace github.com/DataDog/dd-trace-go/contrib/valyala/fasthttp/v2 => ../../contrib/valyala/fasthttp

replace github.com/DataDog/dd-trace-go/contrib/labstack/echo.v4/otelc/v2 => ../../contrib/labstack/echo.v4/otelc

replace github.com/DataDog/dd-trace-go/contrib/labstack/echo.v4/v2 => ../../contrib/labstack/echo.v4

replace github.com/DataDog/dd-trace-go/contrib/labstack/echo.v5/otelc/v2 => ../../contrib/labstack/echo.v5/otelc

replace github.com/DataDog/dd-trace-go/contrib/labstack/echo.v5/v2 => ../../contrib/labstack/echo.v5

replace github.com/DataDog/dd-trace-go/contrib/aws/aws-sdk-go/otelc/v2 => ../../contrib/aws/aws-sdk-go/otelc

replace github.com/DataDog/dd-trace-go/contrib/aws/aws-sdk-go/v2 => ../../contrib/aws/aws-sdk-go
