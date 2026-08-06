package compiler

import (
	"testing"

	"github.com/gecburton/composey/internal/models"
)

// --- test_schedule.py: cron/rate parsing, expanded --------------------------

func TestParseScheduleCronNormalization(t *testing.T) {
	t.Parallel()
	cases := []struct {
		input    string
		expected string
	}{
		{"0 2 * * *", "0 2 * * *"},
		{"cron(0 2 * * ? *)", "0 2 * * *"},
		{"cron(0 2 ? * MON *)", "0 2 * * MON"},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			schedule, err := ParseSchedule(tc.input)
			if err != nil {
				t.Fatalf("ParseSchedule(%q) failed: %v", tc.input, err)
			}
			cron, ok := schedule.(*models.CronSchedule)
			if !ok {
				t.Fatalf("expected a CronSchedule, got %T", schedule)
			}
			if cron.Expression != tc.expected {
				t.Errorf("Expression = %q, want %q", cron.Expression, tc.expected)
			}
		})
	}
}

func TestParseScheduleIntervals(t *testing.T) {
	t.Parallel()
	cases := []struct {
		input string
		value int
		unit  string
	}{
		{"every 1 hour", 1, "hours"},
		{"every hour", 1, "hours"},
		{"rate(15 minutes)", 15, "minutes"},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			schedule, err := ParseSchedule(tc.input)
			if err != nil {
				t.Fatalf("ParseSchedule(%q) failed: %v", tc.input, err)
			}
			rate, ok := schedule.(*models.RateSchedule)
			if !ok {
				t.Fatalf("expected a RateSchedule, got %T", schedule)
			}
			if rate.Value != tc.value {
				t.Errorf("Value = %d, want %d", rate.Value, tc.value)
			}
			if string(rate.Unit) != tc.unit {
				t.Errorf("Unit = %q, want %q", rate.Unit, tc.unit)
			}
		})
	}
}

func TestParseScheduleRejectsMalformed(t *testing.T) {
	t.Parallel()
	cases := []string{"", "0 2 * *", "* * * * * * *", "hourly", "every fortnight"}
	for _, input := range cases {
		t.Run(input, func(t *testing.T) {
			t.Parallel()
			if _, err := ParseSchedule(input); err == nil {
				t.Errorf("ParseSchedule(%q) expected an error, got nil", input)
			}
		})
	}
}

func TestNormalizerReadsScheduleFromComposeFile(t *testing.T) {
	t.Parallel()
	app := &models.ComposeApplication{
		Services: map[string]models.ComposeService{
			"cleanup": {
				Image:     "myapp/cleanup",
				XComposey: map[string]interface{}{"schedule": "every 6 hours"},
			},
		},
	}
	result, err := Normalize(app, "p")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}
	rate, ok := result.Services[0].Schedule.(*models.RateSchedule)
	if !ok {
		t.Fatalf("expected a RateSchedule, got %T", result.Services[0].Schedule)
	}
	if rate.Value != 6 || string(rate.Unit) != "hours" {
		t.Errorf("Schedule = %+v, want every 6 hours", rate)
	}
}
