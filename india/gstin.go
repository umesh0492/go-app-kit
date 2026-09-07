package india

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var (
	ErrInvalidGSTINLength   = errors.New("gstin must be exactly 15 characters")
	ErrInvalidGSTINFormat   = errors.New("gstin format is invalid")
	ErrInvalidGSTINChecksum = errors.New("gstin checksum mismatch")
	ErrInvalidStateCode     = errors.New("gstin state code is invalid")

	gstinRegex = regexp.MustCompile(`^[0-9]{2}[A-Z]{5}[0-9]{4}[A-Z]{1}[1-9A-Z]{1}Z[0-9A-Z]{1}$`)

	// Valid Indian state and union territory GST codes (01-38, 97, 99)
	validStateCodes = map[string]string{
		"01": "Jammu and Kashmir",
		"02": "Himachal Pradesh",
		"03": "Punjab",
		"04": "Chandigarh",
		"05": "Uttarakhand",
		"06": "Haryana",
		"07": "Delhi",
		"08": "Rajasthan",
		"09": "Uttar Pradesh",
		"10": "Bihar",
		"11": "Sikkim",
		"12": "Arunachal Pradesh",
		"13": "Nagaland",
		"14": "Manipur",
		"15": "Mizoram",
		"16": "Tripura",
		"17": "Meghalaya",
		"18": "Assam",
		"19": "West Bengal",
		"20": "Jharkhand",
		"21": "Odisha",
		"22": "Chhattisgarh",
		"23": "Madhya Pradesh",
		"24": "Gujarat",
		"26": "Dadra and Nagar Haveli and Daman and Diu",
		"27": "Maharashtra",
		"29": "Karnataka",
		"30": "Goa",
		"31": "Lakshadweep",
		"32": "Kerala",
		"33": "Tamil Nadu",
		"34": "Puducherry",
		"35": "Andaman and Nicobar Islands",
		"36": "Telangana",
		"37": "Andhra Pradesh",
		"38": "Ladakh",
		"97": "Other Territory",
		"99": "Centre Jurisdiction",
	}

	gstCharMap = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"
)

// GSTINDetails represents parsed information from a valid GSTIN.
type GSTINDetails struct {
	GSTIN      string
	StateCode  string
	StateName  string
	PAN        string
	EntityNum  string
	CheckDigit string
}

// ValidateGSTIN checks length, regex format, state code, and the official mod-36 checksum.
func ValidateGSTIN(gstin string) error {
	clean := strings.ToUpper(strings.TrimSpace(gstin))
	if len(clean) != 15 {
		return ErrInvalidGSTINLength
	}

	if !gstinRegex.MatchString(clean) {
		return ErrInvalidGSTINFormat
	}

	stateCode := clean[:2]
	if _, ok := validStateCodes[stateCode]; !ok {
		return fmt.Errorf("%w: %s", ErrInvalidStateCode, stateCode)
	}

	expectedChecksum := CalculateGSTINCheckDigit(clean[:14])
	if clean[14] != expectedChecksum {
		return fmt.Errorf("%w: expected %c, got %c", ErrInvalidGSTINChecksum, expectedChecksum, clean[14])
	}

	return nil
}

// IsValidGSTIN returns true if the GSTIN passes all validation rules.
func IsValidGSTIN(gstin string) bool {
	return ValidateGSTIN(gstin) == nil
}

// ParseGSTIN validates and decomposes a GSTIN into its constituent components.
func ParseGSTIN(gstin string) (*GSTINDetails, error) {
	if err := ValidateGSTIN(gstin); err != nil {
		return nil, err
	}
	clean := strings.ToUpper(strings.TrimSpace(gstin))
	stateCode := clean[:2]

	return &GSTINDetails{
		GSTIN:      clean,
		StateCode:  stateCode,
		StateName:  validStateCodes[stateCode],
		PAN:        clean[2:12],
		EntityNum:  string(clean[12]),
		CheckDigit: string(clean[14]),
	}, nil
}

// CalculateGSTINCheckDigit computes the official mod-36 checksum character for the first 14 chars.
func CalculateGSTINCheckDigit(input14 string) byte {
	clean := strings.ToUpper(strings.TrimSpace(input14))
	if len(clean) != 14 {
		return '0'
	}

	sum := 0
	for i := 0; i < 14; i++ {
		char := clean[i]
		val := strings.IndexByte(gstCharMap, char)
		if val == -1 {
			return '0'
		}

		factor := 1
		if i%2 != 0 {
			factor = 2
		}

		product := val * factor
		quotient := product / 36
		remainder := product % 36
		sum += quotient + remainder
	}

	checkCode := (36 - (sum % 36)) % 36
	return gstCharMap[checkCode]
}
