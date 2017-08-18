package sql

import (
	"database/sql/driver"
	"errors"
	"sort"
	"strings"

	"github.com/DataDog/dd-trace-go/tracer"
)

func newDSNAndService(dsn, service string) string {
	return dsn + "|" + service
}

func parseDSNAndService(dsnAndService string) (dsn, service string) {
	tab := strings.Split(dsnAndService, "|")
	return tab[0], tab[1]
}

// namedValueToValue is a helper function copied from the database/sql package.
func namedValueToValue(named []driver.NamedValue) ([]driver.Value, error) {
	dargs := make([]driver.Value, len(named))
	for n, param := range named {
		if len(param.Name) > 0 {
			return nil, errors.New("sql: driver does not support the use of Named Parameters")
		}
		dargs[n] = param.Value
	}
	return dargs, nil
}

// stringInSlice returns true if the string s is in the list.
func stringInSlice(list []string, s string) bool {
	sort.Strings(list)
	i := sort.SearchStrings(list, s)
	return i < len(list) && list[i] == s
}

// Get tracer from variadic arguments
func getTracer(tracers []*tracer.Tracer) *tracer.Tracer {
	var t *tracer.Tracer
	if len(tracers) == 0 || (len(tracers) > 0 && tracers[0] == nil) {
		t = tracer.DefaultTracer
	} else {
		t = tracers[0]
	}
	return t
}
