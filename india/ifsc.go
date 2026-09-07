package india

import (
	"errors"
	"regexp"
	"strings"
)

var (
	ErrInvalidIFSCLength = errors.New("ifsc must be exactly 11 characters")
	ErrInvalidIFSCFormat = errors.New("ifsc format is invalid (4 bank letters + 0 + 6 branch alphanumeric)")

	// 4 letters (Bank), '0' (5th character reserved), 6 alphanumeric (Branch)
	ifscRegex = regexp.MustCompile(`^[A-Z]{4}0[A-Z0-9]{6}$`)
)

// ValidateIFSC checks the structural validity of an Indian Financial System Code.
func ValidateIFSC(code string) error {
	clean := strings.ToUpper(strings.TrimSpace(code))
	if len(clean) != 11 {
		return ErrInvalidIFSCLength
	}

	if !ifscRegex.MatchString(clean) {
		return ErrInvalidIFSCFormat
	}

	return nil
}

// IsValidIFSC returns true if the IFSC code matches RBI format.
func IsValidIFSC(code string) bool {
	return ValidateIFSC(code) == nil
}

// GetBankCode extracts the 4-letter bank identifier prefix from an IFSC code.
func GetBankCode(code string) (string, error) {
	if err := ValidateIFSC(code); err != nil {
		return "", err
	}
	clean := strings.ToUpper(strings.TrimSpace(code))
	return clean[:4], nil
}

// GetBranchCode extracts the 6-character branch identifier from an IFSC code.
func GetBranchCode(code string) (string, error) {
	if err := ValidateIFSC(code); err != nil {
		return "", err
	}
	clean := strings.ToUpper(strings.TrimSpace(code))
	return clean[5:], nil
}
