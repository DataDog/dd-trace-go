package sql

import (
	"database/sql/driver"
	"errors"
	"fmt"
	"reflect"
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

// getTracer returns either the tracer passed as the last argument or a default tracer.
func getTracer(tracers []*tracer.Tracer) *tracer.Tracer {
	var t *tracer.Tracer
	if len(tracers) == 0 || (len(tracers) > 0 && tracers[0] == nil) {
		t = tracer.DefaultTracer
	} else {
		t = tracers[0]
	}
	return t
}

// DriverName returns the generic driver name for the drivers supported by this package.
func DriverName(driver driver.Driver) (string, error) {
	if driver == nil {
		return "", errors.New("Driver is nil.")
	}
	driverType := fmt.Sprintf("%s", reflect.TypeOf(driver))
	switch driverType {
	case "*mysql.MySQLDriver":
		return "mysql", nil
	case "*pq.Driver":
		return "postgres", nil
	default:
		return "", errors.New("Driver not yet supported.")
	}
}

// TracedName add the suffix "Traced" to the driver name.
func TracedName(driverName string) string {
	return driverName + "Traced"
}

// UntracedName removes the suffix "Traced" from the traced driver name.
func UntracedName(tracedDriverName string) string {
	return strings.TrimSuffix(tracedDriverName, "Traced")
}
