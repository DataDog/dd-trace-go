package faketelemetrylog

func ReportError(msg string, err error, opts ...any) {}
func ReportPanic(recovered any, msg string)           {}
