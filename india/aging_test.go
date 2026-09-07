package india_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/umesh0492/go-app-kit/india"
)

func TestAgingBucket(t *testing.T) {
	assert.Equal(t, "Current", india.AgingBucket(0))
	assert.Equal(t, "Current", india.AgingBucket(-5))
	assert.Equal(t, "1-30", india.AgingBucket(1))
	assert.Equal(t, "1-30", india.AgingBucket(30))
	assert.Equal(t, "31-60", india.AgingBucket(31))
	assert.Equal(t, "31-60", india.AgingBucket(60))
	assert.Equal(t, "61-90", india.AgingBucket(61))
	assert.Equal(t, "61-90", india.AgingBucket(90))
	assert.Equal(t, "90+", india.AgingBucket(91))
	assert.Equal(t, "90+", india.AgingBucket(120))
}

func TestDaysOverdue(t *testing.T) {
	ref := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)

	// Due in the future
	future := time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC)
	assert.Equal(t, 0, india.DaysOverdue(future, ref))

	// Due today
	today := time.Date(2026, 4, 15, 8, 0, 0, 0, time.UTC)
	assert.Equal(t, 0, india.DaysOverdue(today, ref))

	// Due 5 days ago
	past := time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC)
	assert.Equal(t, 5, india.DaysOverdue(past, ref))

	// UTC shift boundary test:
	// In UTC: ref is April 14 20:00:00 UTC (April 14)
	// In IST: ref is April 15 01:30:00 IST (April 15)
	// dueDate is April 14 10:00:00 UTC (April 14 15:30:00 IST - April 14)
	// In IST, dueDate is 1 day overdue. (In naive UTC calculation, both were April 14 -> 0 days overdue).
	refUTCShift := time.Date(2026, 4, 14, 20, 0, 0, 0, time.UTC)
	dueUTCShift := time.Date(2026, 4, 14, 10, 0, 0, 0, time.UTC)
	assert.Equal(t, 1, india.DaysOverdue(dueUTCShift, refUTCShift))
}
