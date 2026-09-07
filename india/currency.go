package india

import (
	"fmt"
	"strings"
)

var (
	ones = []string{
		"", "One", "Two", "Three", "Four", "Five", "Six", "Seven", "Eight", "Nine",
		"Ten", "Eleven", "Twelve", "Thirteen", "Fourteen", "Fifteen", "Sixteen",
		"Seventeen", "Eighteen", "Nineteen",
	}

	tens = []string{
		"", "", "Twenty", "Thirty", "Forty", "Fifty", "Sixty", "Seventy", "Eighty", "Ninety",
	}
)

// FormatINRPaise formats an exact integer amount in paise (1 INR = 100 paise)
// into Indian currency format with comma placements and two decimal digits.
// e.g. 123456789 -> "12,34,567.89", -5000 -> "-50.00"
func FormatINRPaise(paise int64) string {
	isNegative := paise < 0
	absPaise := paise
	if isNegative {
		absPaise = -absPaise
	}

	intPart := absPaise / 100
	fracPart := absPaise % 100

	intStr := fmt.Sprintf("%d", intPart)
	n := len(intStr)

	var formattedInt string
	if n <= 3 {
		formattedInt = intStr
	} else {
		// Indian numbering system: last 3 digits, then groups of 2 digits
		last3 := intStr[n-3:]
		remaining := intStr[:n-3]

		var parts []string
		for len(remaining) > 2 {
			parts = append([]string{remaining[len(remaining)-2:]}, parts...)
			remaining = remaining[:len(remaining)-2]
		}
		if len(remaining) > 0 {
			parts = append([]string{remaining}, parts...)
		}
		formattedInt = strings.Join(parts, ",") + "," + last3
	}

	if isNegative && (intPart > 0 || fracPart > 0) {
		formattedInt = "-" + formattedInt
	}

	return fmt.Sprintf("%s.%02d", formattedInt, fracPart)
}

// AmountToWordsINR converts an integer rupee amount into words following the Indian numbering system.
// Supports amounts up to arbitrary Crores (Crore, Lakh, Thousand, Hundred).
func AmountToWordsINR(amount int64) string {
	if amount == 0 {
		return "Zero Rupees Only"
	}

	isNegative := amount < 0
	if isNegative {
		amount = -amount
	}

	words := amountToWordsBase(amount)
	resStr := words + " Rupees Only"
	if isNegative {
		return "Minus " + resStr
	}
	return resStr
}

func amountToWordsBase(amount int64) string {
	if amount == 0 {
		return ""
	}

	crores := amount / 10000000
	remainder := amount % 10000000

	lakhs := remainder / 100000
	remainder = remainder % 100000

	thousands := remainder / 1000
	remainder = remainder % 1000

	hundreds := remainder / 100
	units := remainder % 100

	var result []string

	if crores > 0 {
		result = append(result, amountToWordsBase(crores)+" Crore")
	}
	if lakhs > 0 {
		result = append(result, helperTwoDigit(int(lakhs))+" Lakh")
	}
	if thousands > 0 {
		result = append(result, helperTwoDigit(int(thousands))+" Thousand")
	}
	if hundreds > 0 {
		result = append(result, ones[hundreds]+" Hundred")
	}
	if units > 0 {
		result = append(result, helperTwoDigit(int(units)))
	}

	return strings.Join(result, " ")
}

func helperTwoDigit(n int) string {
	if n < 20 {
		return ones[n]
	}
	ten := tens[n/10]
	one := ones[n%10]
	if one != "" {
		return ten + " " + one
	}
	return ten
}
