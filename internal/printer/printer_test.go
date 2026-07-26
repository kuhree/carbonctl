package printer_test

import (
	"testing"

	"git.kuhree.com/kuhree.com/fleet/bin/carbonctl/internal/printer"
)

func TestStatusNormalizationAcceptsDocumentedAlternateNesting(t *testing.T) {
	status, err := printer.ParseStatus([]byte(`{
		"CurrentStatus":[99],
		"PrintInfo":{"TaskID":"alternate-task","FileName":"alternate.gcode",
			"CurrentLayer":4,"TotalLayers":9},
		"Status":{"TempOfNozzle":201,"TempOfHotbed":58}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if status.State != "unknown" || status.TaskID != "alternate-task" ||
		status.Filename != "alternate.gcode" || status.CurrentLayer != 4 ||
		status.TotalLayers != 9 || status.Temperatures.Nozzle != 201 ||
		status.Temperatures.Bed != 58 {
		t.Fatalf("normalized status = %#v", status)
	}
}
