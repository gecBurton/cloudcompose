package azure

import (
	"testing"

	"github.com/gecburton/cloudcompose/internal/models"
)

// --- Azure schedule cron conversion: entirely untested before this pass.

func TestCronExpressionAzure_RateSchedules(t *testing.T) {
	t.Parallel()
	// Every case pinned against a known-correct cron expression for the
	// given rate, not just this implementation's own idea of the conversion.
	cases := []struct {
		value int
		unit  models.RateUnit
		want  string
	}{
		{1, models.RateUnitMinutes, "* * * * *"},
		{5, models.RateUnitMinutes, "*/5 * * * *"},
		{30, models.RateUnitMinutes, "*/30 * * * *"},
		{1, models.RateUnitHours, "0 * * * *"},
		{6, models.RateUnitHours, "0 */6 * * *"},
		{1, models.RateUnitDays, "0 0 * * *"},
	}
	for _, tc := range cases {
		got, err := cronExpressionAzure(models.RateSchedule{Value: tc.value, Unit: tc.unit})
		if err != nil {
			t.Errorf("value=%d unit=%s: unexpected error: %v", tc.value, tc.unit, err)
			continue
		}
		if got != tc.want {
			t.Errorf("value=%d unit=%s: got %q, want %q", tc.value, tc.unit, got, tc.want)
		}
	}
}

func TestCronExpressionAzure_RejectsUnevenIntervals(t *testing.T) {
	t.Parallel()
	cases := []struct {
		value int
		unit  models.RateUnit
	}{
		{7, models.RateUnitMinutes},
		{7, models.RateUnitHours},
		{2, models.RateUnitDays},
	}
	for _, tc := range cases {
		_, err := cronExpressionAzure(models.RateSchedule{Value: tc.value, Unit: tc.unit})
		if err == nil {
			t.Errorf("value=%d unit=%s: expected an error, got none", tc.value, tc.unit)
		}
	}
}

func TestCronExpressionAzure_AcceptsPointerSchedules(t *testing.T) {
	t.Parallel()
	// Mirrors AWS's own regression test for this exact bug class: the
	// real normalizer produces *models.RateSchedule/*models.CronSchedule
	// (pointers), not value types.
	rate := &models.RateSchedule{Value: 1, Unit: models.RateUnitHours}
	if got, err := cronExpressionAzure(rate); err != nil || got != "0 * * * *" {
		t.Errorf("got %q, err %v, want '0 * * * *', nil", got, err)
	}
	cron := &models.CronSchedule{Expression: "0 5 * * *"}
	if got, err := cronExpressionAzure(cron); err != nil || got != "0 5 * * *" {
		t.Errorf("got %q, err %v, want '0 5 * * *', nil", got, err)
	}
}
