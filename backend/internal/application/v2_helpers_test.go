package application_test

import "fmt"

func fmtSprint(v any) string { return fmt.Sprintf("%+v", v) }
