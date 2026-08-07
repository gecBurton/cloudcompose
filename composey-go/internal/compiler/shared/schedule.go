package shared

import "github.com/gecburton/composey/internal/models"

// AsRateSchedule reports whether schedule is a RateSchedule (by value or
// pointer), returning the concrete value if so. Cloud-agnostic: used both
// by inference (Azure's cron-translation, which needs to know whether the
// interval divides evenly into a valid cron field) and by the
// cloud-agnostic --explain reporting.
func AsRateSchedule(schedule models.Schedule) (models.RateSchedule, bool) {
	switch s := schedule.(type) {
	case models.RateSchedule:
		return s, true
	case *models.RateSchedule:
		return *s, true
	default:
		return models.RateSchedule{}, false
	}
}

// AsCronSchedule reports whether schedule is a CronSchedule (by value or
// pointer), returning the concrete value if so.
func AsCronSchedule(schedule models.Schedule) (models.CronSchedule, bool) {
	switch s := schedule.(type) {
	case models.CronSchedule:
		return s, true
	case *models.CronSchedule:
		return *s, true
	default:
		return models.CronSchedule{}, false
	}
}
