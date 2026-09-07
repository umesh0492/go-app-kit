package india

import (
	"fmt"
	"time"
)

// FinancialQuarter represents Q1, Q2, Q3, or Q4 of the Indian Financial Year.
type FinancialQuarter string

const (
	Q1 FinancialQuarter = "Q1" // April - June
	Q2 FinancialQuarter = "Q2" // July - September
	Q3 FinancialQuarter = "Q3" // October - December
	Q4 FinancialQuarter = "Q4" // January - March
)

var istLocation = func() *time.Location {
	loc, err := time.LoadLocation("Asia/Kolkata")
	if err == nil {
		return loc
	}
	return time.FixedZone("IST", 5*3600+30*60)
}()

// ISTLocation returns the *time.Location representing Asia/Kolkata (IST: UTC+5:30).
func ISTLocation() *time.Location {
	return istLocation
}

// FinancialYear contains Indian fiscal year details.
type FinancialYear struct {
	Label     string           // e.g., "FY 2026-27"
	ShortCode string           // e.g., "FY26-27"
	StartYear int              // e.g., 2026
	EndYear   int              // e.g., 2027
	StartDate time.Time        // April 1, 00:00:00 IST
	EndDate   time.Time        // March 31, 23:59:59 IST
	Quarter   FinancialQuarter // Q1, Q2, Q3, or Q4
}

// GetFinancialYear calculates the Indian Financial Year and Quarter for any given timestamp.
// Converts timestamps to Asia/Kolkata IST location before computing calendar year, month, or day boundaries
// to prevent 5.5h/day UTC shift errors.
// In India, the Financial Year begins on April 1st and ends on March 31st.
func GetFinancialYear(t time.Time) FinancialYear {
	tIST := t.In(istLocation)
	year := tIST.Year()
	month := tIST.Month()

	var startYear, endYear int
	var q FinancialQuarter

	switch {
	case month >= time.April && month <= time.June:
		startYear = year
		endYear = year + 1
		q = Q1
	case month >= time.July && month <= time.September:
		startYear = year
		endYear = year + 1
		q = Q2
	case month >= time.October && month <= time.December:
		startYear = year
		endYear = year + 1
		q = Q3
	default: // January - March
		startYear = year - 1
		endYear = year
		q = Q4
	}

	start := time.Date(startYear, time.April, 1, 0, 0, 0, 0, istLocation)
	end := time.Date(endYear, time.March, 31, 23, 59, 59, 999999999, istLocation)

	shortStart := startYear % 100
	shortEnd := endYear % 100

	return FinancialYear{
		Label:     fmt.Sprintf("FY %d-%02d", startYear, shortEnd),
		ShortCode: fmt.Sprintf("FY%02d-%02d", shortStart, shortEnd),
		StartYear: startYear,
		EndYear:   endYear,
		StartDate: start,
		EndDate:   end,
		Quarter:   q,
	}
}

// CurrentFinancialYear returns the Indian Financial Year for the current moment in time.
func CurrentFinancialYear() FinancialYear {
	return GetFinancialYear(time.Now())
}
