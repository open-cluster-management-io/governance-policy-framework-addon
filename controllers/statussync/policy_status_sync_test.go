// Copyright Contributors to the Open Cluster Management project
package statussync

import (
	"testing"
)

func TestParseTimestampFromEventName(t *testing.T) {
	output, err := parseTimestampFromEventName("event.17b80d88a995e12c")
	if err != nil {
		t.Errorf("Expected no error but got: %v", err)
	} else if output.Time.UnixNano() != 1709130939198988588 {
		t.Errorf("Expected 1709130939198988588 but got: %d", output.Time.UnixNano())
	}

	output, err = parseTimestampFromEventName("event.with-no-timestamp")
	if err == nil {
		t.Errorf("Expected an error but got none: %s", output)
	}
}
