package tomte

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"
)

func TestInfo(t *testing.T) {
	a := New("chaser",
		Model("http://localhost", "glm", ""),
		Tool("add", "Add two numbers", add),
		Cron("0 9 * * 1-5", "chase"),
		Price(1, 2), MaxCost(0.5, 5),
	)
	a.now = func() time.Time { return time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC) } // a Friday

	var out bytes.Buffer
	if code := a.printInfo(&out); code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	if bytes.Count(out.Bytes(), []byte("\n")) != 1 {
		t.Errorf("info isn't one line: %q", out.String())
	}
	var in info
	if err := json.Unmarshal(out.Bytes(), &in); err != nil {
		t.Fatal(err)
	}
	if in.Name != "chaser" || in.Model != "glm" || in.Channel != "stdout" || len(in.Tools) != 1 || in.MaxDay != 5 {
		t.Errorf("info = %+v", in)
	}
	if len(in.Schedules) != 1 || !in.Schedules[0].Next.Equal(time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)) {
		t.Errorf("schedules = %+v", in.Schedules)
	}
}

// Info reports mistakes in the options rather than failing on them, so
// the tomte command can show them.
func TestInfoReportsMistakes(t *testing.T) {
	a := New("broken", Cron("61 * * * *", "never"))
	var out bytes.Buffer
	if code := a.printInfo(&out); code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	var in info
	if err := json.Unmarshal(out.Bytes(), &in); err != nil {
		t.Fatal(err)
	}
	if len(in.Errors) != 2 { // no model, and the bad minute
		t.Errorf("errors = %q", in.Errors)
	}
}
