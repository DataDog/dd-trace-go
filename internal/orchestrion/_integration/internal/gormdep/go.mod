// A module outside dd-trace-go's paths that opens gorm connections, standing in
// for a third-party library. otelc must instrument call sites in dependencies,
// not only in the module being built.
module example.com/gormdep

go 1.26.0

require (
	gorm.io/driver/sqlite v1.5.7
	gorm.io/gorm v1.25.12
)

require (
	github.com/jinzhu/inflection v1.0.0 // indirect
	github.com/jinzhu/now v1.1.5 // indirect
	github.com/mattn/go-sqlite3 v1.14.22 // indirect
	golang.org/x/text v0.14.0 // indirect
)
