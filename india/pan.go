package india

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var (
	ErrInvalidPANLength  = errors.New("pan must be exactly 10 characters")
	ErrInvalidPANFormat  = errors.New("pan format is invalid")
	ErrUnknownEntityType = errors.New("unknown pan entity type")

	panRegex = regexp.MustCompile(`^[A-Z]{3}[A-Z]{1}[A-Z]{1}[0-9]{4}[A-Z]{1}$`)

	// Mapping of 4th character of PAN to legal entity type in India
	entityTypes = map[byte]string{
		'A': "Association of Persons (AOP)",
		'B': "Body of Individuals (BOI)",
		'C': "Company",
		'F': "Firm / Limited Liability Partnership (LLP)",
		'G': "Government Agency",
		'H': "Hindu Undivided Family (HUF)",
		'J': "Artificial Juridical Person",
		'L': "Local Authority",
		'P': "Individual (Person)",
		'T': "Trust",
	}
)

// PANDetails represents parsed information from a valid PAN.
type PANDetails struct {
	PAN            string
	EntityTypeCode string
	EntityTypeName string
	SurnameInitial string
}

// ValidatePAN verifies the structural validity of a 10-character Indian PAN card number.
func ValidatePAN(pan string) error {
	clean := strings.ToUpper(strings.TrimSpace(pan))
	if len(clean) != 10 {
		return ErrInvalidPANLength
	}

	if !panRegex.MatchString(clean) {
		return ErrInvalidPANFormat
	}

	fourthChar := clean[3]
	if _, ok := entityTypes[fourthChar]; !ok {
		return fmt.Errorf("%w: '%c' is not a valid PAN entity type", ErrUnknownEntityType, fourthChar)
	}

	return nil
}

// IsValidPAN returns true if the PAN passes format and entity checks.
func IsValidPAN(pan string) bool {
	return ValidatePAN(pan) == nil
}

// ParsePAN validates and parses the PAN card components.
func ParsePAN(pan string) (*PANDetails, error) {
	if err := ValidatePAN(pan); err != nil {
		return nil, err
	}
	clean := strings.ToUpper(strings.TrimSpace(pan))
	fourthChar := clean[3]

	return &PANDetails{
		PAN:            clean,
		EntityTypeCode: string(fourthChar),
		EntityTypeName: entityTypes[fourthChar],
		SurnameInitial: string(clean[4]),
	}, nil
}
