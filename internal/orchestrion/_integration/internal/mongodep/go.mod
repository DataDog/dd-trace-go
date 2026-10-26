// A module outside dd-trace-go's paths that creates mongo clients, standing in
// for a third-party library. otelc must rewrite call sites in dependencies, not
// only in the module being built.
module example.com/mongodep

go 1.26.0

require (
	go.mongodb.org/mongo-driver v1.17.7
	go.mongodb.org/mongo-driver/v2 v2.4.2
)

require (
	github.com/golang/snappy v1.0.0 // indirect
	github.com/klauspost/compress v1.16.7 // indirect
	github.com/montanaflynn/stats v0.7.1 // indirect
	github.com/xdg-go/pbkdf2 v1.0.0 // indirect
	github.com/xdg-go/scram v1.1.2 // indirect
	github.com/xdg-go/stringprep v1.0.4 // indirect
	github.com/youmark/pkcs8 v0.0.0-20240726163527-a2c0da244d78 // indirect
	golang.org/x/crypto v0.33.0 // indirect
	golang.org/x/sync v0.11.0 // indirect
	golang.org/x/text v0.22.0 // indirect
)
