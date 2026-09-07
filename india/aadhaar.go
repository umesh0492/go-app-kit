package india

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var (
	ErrInvalidAadhaarLength   = errors.New("aadhaar number must be exactly 12 digits")
	ErrInvalidAadhaarFormat   = errors.New("aadhaar number must contain only numeric digits")
	ErrInvalidAadhaarPrefix   = errors.New("aadhaar number cannot start with 0 or 1")
	ErrInvalidAadhaarChecksum = errors.New("aadhaar checksum validation failed (Verhoeff check)")

	aadhaarRegex = regexp.MustCompile(`^[2-9]{1}[0-9]{11}$`)

	// Verhoeff algorithm multiplication table (d)
	verhoeffD = [10][10]int{
		{0, 1, 2, 3, 4, 5, 6, 7, 8, 9},
		{1, 2, 3, 4, 0, 6, 7, 8, 9, 5},
		{2, 3, 4, 0, 1, 7, 8, 9, 5, 6},
		{3, 4, 0, 1, 2, 8, 9, 5, 6, 7},
		{4, 0, 1, 2, 3, 9, 5, 6, 7, 8},
		{5, 9, 8, 7, 6, 0, 4, 3, 2, 1},
		{6, 5, 9, 8, 7, 1, 0, 4, 3, 2},
		{7, 6, 5, 9, 8, 2, 1, 0, 4, 3},
		{8, 7, 6, 5, 9, 3, 2, 1, 0, 4},
		{9, 8, 7, 6, 5, 4, 3, 2, 1, 0},
	}

	// Verhoeff algorithm permutation table (p)
	verhoeffP = [8][10]int{
		{0, 1, 2, 3, 4, 5, 6, 7, 8, 9},
		{1, 5, 7, 6, 2, 8, 3, 0, 9, 4},
		{5, 8, 0, 3, 7, 9, 6, 1, 4, 2},
		{8, 9, 1, 6, 0, 4, 3, 5, 2, 7},
		{9, 4, 5, 3, 1, 2, 6, 8, 7, 0},
		{4, 2, 8, 6, 5, 7, 3, 9, 0, 1},
		{2, 7, 9, 3, 8, 0, 6, 4, 1, 5},
		{7, 0, 4, 6, 9, 1, 3, 2, 5, 8},
	}
)

// ValidateAadhaar verifies that an Aadhaar number is 12 digits, does not start with 0 or 1,
// and satisfies the official UIDAI Verhoeff dihedral D5 checksum.
func ValidateAadhaar(aadhaar string) error {
	clean := strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(aadhaar), "-", ""), " ", "")
	if len(clean) != 12 {
		return ErrInvalidAadhaarLength
	}

	if clean[0] == '0' || clean[0] == '1' {
		return ErrInvalidAadhaarPrefix
	}

	if !aadhaarRegex.MatchString(clean) {
		return ErrInvalidAadhaarFormat
	}

	// Verhoeff Checksum validation
	c := 0
	length := len(clean)
	for i := 0; i < length; i++ {
		// Read from right to left (index from right: 0, 1, 2, ...)
		digit, err := strconv.Atoi(string(clean[length-1-i]))
		if err != nil {
			return ErrInvalidAadhaarFormat
		}
		c = verhoeffD[c][verhoeffP[i%8][digit]]
	}

	if c != 0 {
		return ErrInvalidAadhaarChecksum
	}

	return nil
}

// IsValidAadhaar returns true if the Aadhaar number passes all UIDAI validation rules.
func IsValidAadhaar(aadhaar string) bool {
	return ValidateAadhaar(aadhaar) == nil
}

// MaskAadhaar formats an Aadhaar number with privacy masking (e.g., "XXXX-XXXX-1234").
func MaskAadhaar(aadhaar string) string {
	clean := strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(aadhaar), "-", ""), " ", "")
	if len(clean) != 12 {
		return aadhaar
	}
	return fmt.Sprintf("XXXX-XXXX-%s", clean[8:])
}

// FormatAadhaar formats an Aadhaar into standard 4-digit groups (e.g., "1234 5678 9012").
func FormatAadhaar(aadhaar string) string {
	clean := strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(aadhaar), "-", ""), " ", "")
	if len(clean) != 12 {
		return aadhaar
	}
	return fmt.Sprintf("%s %s %s", clean[:4], clean[4:8], clean[8:])
}
