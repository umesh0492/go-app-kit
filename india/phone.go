package india

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var (
	ErrInvalidPhoneFormat = errors.New("indian mobile number must be 10 digits starting with 6, 7, 8, or 9")

	phoneCleanRegex  = regexp.MustCompile(`[^\d]`)
	validMobileRegex = regexp.MustCompile(`^[6-9]\d{9}$`)
)

// ValidatePhone validates an Indian 10-digit mobile number with optional +91, 91, or 0 prefix.
func ValidatePhone(phone string) error {
	digits := ExtractMobileDigits(phone)
	if !validMobileRegex.MatchString(digits) {
		return ErrInvalidPhoneFormat
	}
	return nil
}

// IsValidPhone returns true if the phone number is a valid Indian mobile number.
func IsValidPhone(phone string) bool {
	return ValidatePhone(phone) == nil
}

// ExtractMobileDigits strips prefixes (+91, 91, 0) and non-numeric characters, returning the 10-digit number.
func ExtractMobileDigits(phone string) string {
	digits := phoneCleanRegex.ReplaceAllString(phone, "")

	// Handle prefixes: +91 (12 digits total) or 91 (12 digits) or 0 (11 digits)
	if len(digits) == 12 && strings.HasPrefix(digits, "91") {
		digits = digits[2:]
	} else if len(digits) == 11 && strings.HasPrefix(digits, "0") {
		digits = digits[1:]
	}

	return digits
}

// FormatE164 formats an Indian mobile number to E.164 international standard (e.g. "+919876543210").
func FormatE164(phone string) (string, error) {
	if err := ValidatePhone(phone); err != nil {
		return "", err
	}
	digits := ExtractMobileDigits(phone)
	return fmt.Sprintf("+91%s", digits), nil
}

// FormatNational formats an Indian mobile number into standard national format (e.g. "098765 43210" or "98765-43210").
func FormatNational(phone string) (string, error) {
	if err := ValidatePhone(phone); err != nil {
		return "", err
	}
	digits := ExtractMobileDigits(phone)
	return fmt.Sprintf("%s-%s", digits[:5], digits[5:]), nil
}
