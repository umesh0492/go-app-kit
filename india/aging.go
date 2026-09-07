package india

import (
	"time"
)

// AgingBucket classifies a past-due number of days into standard Indian accounting AP/AR aging buckets.
// Buckets: Current (<= 0), 1–30, 31–60, 61–90, 90+.
func AgingBucket(daysOverdue int) string {
	switch {
	case daysOverdue <= 0:
		return "Current"
	case daysOverdue <= 30:
		return "1-30"
	case daysOverdue <= 60:
		return "31-60"
	case daysOverdue <= 90:
		return "61-90"
	default:
		return "90+"
	}
}

// DaysOverdue calculates how many calendar days dueDate is past the reference time (usually today).
// Timestamps are converted to Asia/Kolkata (IST) location before computing day boundaries,
// preventing 5.5h/day UTC shift errors across midnight.
// Returns 0 if dueDate is in the future or on the same calendar day.
func DaysOverdue(dueDate, reference time.Time) int {
	dueIST := dueDate.In(istLocation)
	refIST := reference.In(istLocation)

	dueMidnight := time.Date(dueIST.Year(), dueIST.Month(), dueIST.Day(), 0, 0, 0, 0, istLocation)
	refMidnight := time.Date(refIST.Year(), refIST.Month(), refIST.Day(), 0, 0, 0, 0, istLocation)

	diff := int(refMidnight.Sub(dueMidnight).Hours() / 24)
	if diff < 0 {
		return 0
	}
	return diff
}
