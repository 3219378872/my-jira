package planning

import (
	"encoding/json"
	"testing"

	"my-jira/apps/api/internal/support/data"
)

func object(value string) data.Object {
	var result data.Object
	_ = json.Unmarshal([]byte(value), &result)
	return result
}

func TestDateUpdateChecksUnchangedBoundary(t *testing.T) {
	existing := object(`{"start_date":"2026-09-01","end_date":"2026-09-09"}`)
	values, err := parse(cycles, object(`{"start_date":"2026-09-10"}`), false)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateDates(cycles, values, existing); err == nil {
		t.Fatal("changing only the start date must still enforce the persisted end date")
	}
	values, err = parse(cycles, object(`{"end_date":null}`), false)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateDates(cycles, values, existing); err != nil {
		t.Fatalf("explicitly clearing the boundary should be supported: %v", err)
	}
}

func TestRejectInvalidDomainValues(t *testing.T) {
	for _, test := range []struct {
		spec    resource
		payload string
	}{{modules, `{"status":"whatever"}`}, {cycles, `{"start_date":"2026-02-30"}`}, {views, `{"layout":"unsupported"}`}, {views, `{"filters":[]}`}, {cycles, `{"name":" "}`}, {cycles, `{"owner_id":null}`}} {
		if _, err := parse(test.spec, object(test.payload), false); err == nil {
			t.Errorf("accepted invalid payload %s", test.payload)
		}
	}
}
