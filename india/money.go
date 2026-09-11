package india

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

var (
	// ErrInvalidMoneyFormat is returned when parsing an invalid monetary string.
	ErrInvalidMoneyFormat = errors.New("invalid money format")
	// ErrDivisionByZero is returned when splitting or dividing money by non-positive divisor.
	ErrDivisionByZero = errors.New("division by zero in money calculation")
)

// Money represents a monetary value in Indian Rupees stored as an exact integer count of paise (1 INR = 100 paise).
// This eliminates IEEE-754 floating-point inaccuracies in financial and accounting operations.
type Money struct {
	paise int64
}

// NewMoney creates a Money instance from an exact integer count of paise.
func NewMoney(paise int64) Money {
	return Money{paise: paise}
}

// NewMoneyFromRupees creates a Money instance from whole rupees (e.g. 500 -> 50,000 paise).
func NewMoneyFromRupees(rupees int64) Money {
	return Money{paise: rupees * 100}
}

// NewMoneyFromFloat creates a Money instance by rounding a float64 amount in rupees to the nearest paise.
// e.g. 15000.50 -> 1500050 paise, 1.995 -> 200 paise.
func NewMoneyFromFloat(amount float64) Money {
	totalPaise := int64(math.Round(amount * 100))
	return Money{paise: totalPaise}
}

// Paise returns the underlying monetary value in paise (minor units).
func (m Money) Paise() int64 {
	return m.paise
}

// Rupees returns the value as whole rupees (truncated towards zero).
func (m Money) Rupees() int64 {
	return m.paise / 100
}

// Float64 converts the Money value to a float64 in rupees (for interop and display).
func (m Money) Float64() float64 {
	return float64(m.paise) / 100.0
}

// IsZero reports whether the money amount is exactly zero.
func (m Money) IsZero() bool {
	return m.paise == 0
}

// IsPositive reports whether the money amount is strictly greater than zero.
func (m Money) IsPositive() bool {
	return m.paise > 0
}

// IsNegative reports whether the money amount is strictly less than zero.
func (m Money) IsNegative() bool {
	return m.paise < 0
}

// Abs returns the absolute value of the Money amount.
func (m Money) Abs() Money {
	if m.paise < 0 {
		return Money{paise: -m.paise}
	}
	return m
}

// Negate returns the negated Money value.
func (m Money) Negate() Money {
	return Money{paise: -m.paise}
}

// Add returns the sum m + other.
func (m Money) Add(other Money) Money {
	return Money{paise: m.paise + other.paise}
}

// Sub returns the difference m - other.
func (m Money) Sub(other Money) Money {
	return Money{paise: m.paise - other.paise}
}

// Mul multiplies m by an integer factor.
func (m Money) Mul(factor int64) Money {
	return Money{paise: m.paise * factor}
}

// MulBasisPoints multiplies m by basis points (1 basis point = 0.01% = 0.0001, 100 bps = 1%).
// e.g. for 9% GST (900 bps), 1000 INR (100000 paise) * 900 / 10000 = 90 INR (9000 paise).
func (m Money) MulBasisPoints(bps int64) Money {
	return Money{paise: int64(math.Round(float64(m.paise*bps) / 10000.0))}
}

// Percentage computes rate% of m (e.g. 9.0 for 9% GST) with standard financial rounding.
func (m Money) Percentage(rate float64) Money {
	return Money{paise: int64(math.Round(float64(m.paise) * (rate / 100.0)))}
}

// Split divides the monetary value into n parts without losing any paise due to integer truncation.
// The sum of the resulting slice is guaranteed to equal m.
func (m Money) Split(n int) ([]Money, error) {
	if n <= 0 {
		return nil, ErrDivisionByZero
	}
	quotient := m.paise / int64(n)
	remainder := m.paise % int64(n)

	parts := make([]Money, n)
	for i := 0; i < n; i++ {
		parts[i] = Money{paise: quotient}
	}
	if remainder > 0 {
		for i := 0; i < int(remainder); i++ {
			parts[i].paise++
		}
	} else if remainder < 0 {
		for i := 0; i < int(-remainder); i++ {
			parts[i].paise--
		}
	}
	return parts, nil
}

// Format returns the standard Indian currency string (e.g. "12,34,567.89").
func (m Money) Format() string {
	return FormatINRPaise(m.paise)
}

// String implements fmt.Stringer, returning the formatted INR currency string.
func (m Money) String() string {
	return m.Format()
}

// Words returns the amount in words following the Indian numbering system.
func (m Money) Words() string {
	return AmountToWordsINR(m.Rupees())
}

// MarshalJSON serializes Money as a JSON object containing integer paise, formatted string, and currency.
// This ensures that no IEEE-754 floating-point numbers are emitted on the wire.
func (m Money) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		AmountPaise int64  `json:"amount_paise"`
		Formatted   string `json:"formatted"`
		Currency    string `json:"currency"`
	}{
		AmountPaise: m.paise,
		Formatted:   m.Format(),
		Currency:    "INR",
	})
}

// UnmarshalJSON unmarshals Money from:
// 1. Structured JSON object: {"amount_paise": 12345, "formatted": "123.45", "currency": "INR"}
// 2. Integer number in paise: 12345
// 3. String formatted currency: "123.45" or "12,34,567.89"
// 4. Legacy JSON float number: 1234.50
func unmarshalMoneyObject(data []byte) (int64, error) {
	var obj struct {
		AmountPaise *int64  `json:"amount_paise"`
		Paise       *int64  `json:"paise"`
		Formatted   *string `json:"formatted"`
	}
	if err := json.Unmarshal(data, &obj); err != nil {
		return 0, fmt.Errorf("%w: %w", ErrInvalidMoneyFormat, err)
	}
	if obj.AmountPaise != nil {
		return *obj.AmountPaise, nil
	}
	if obj.Paise != nil {
		return *obj.Paise, nil
	}
	if obj.Formatted != nil {
		clean := strings.ReplaceAll(*obj.Formatted, ",", "")
		f, err := strconv.ParseFloat(clean, 64)
		if err != nil {
			return 0, fmt.Errorf("%w: %w", ErrInvalidMoneyFormat, err)
		}
		return int64(math.Round(f * 100)), nil
	}
	return 0, ErrInvalidMoneyFormat
}

// UnmarshalJSON unmarshals Money from:
// 1. Structured JSON object: {"amount_paise": 12345, "formatted": "123.45", "currency": "INR"}
// 2. Integer number in paise: 12345
// 3. String formatted currency: "123.45" or "12,34,567.89"
// 4. Legacy JSON float number: 1234.50
func (m *Money) UnmarshalJSON(data []byte) error {
	s := strings.TrimSpace(string(data))
	if s == "null" || s == "" {
		m.paise = 0
		return nil
	}

	// 1. Structured JSON object
	if strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}") {
		p, err := unmarshalMoneyObject(data)
		if err != nil {
			return err
		}
		m.paise = p
		return nil
	}

	// 2. Quoted string representation
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
		s = strings.ReplaceAll(s, ",", "")
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidMoneyFormat, err)
		}
		m.paise = int64(math.Round(f * 100))
		return nil
	}

	// 3. Number: integer paise or decimal float
	if !strings.Contains(s, ".") {
		p, err := strconv.ParseInt(s, 10, 64)
		if err == nil {
			m.paise = p
			return nil
		}
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidMoneyFormat, err)
	}
	m.paise = int64(math.Round(f * 100))
	return nil
}

// Value implements driver.Valuer to persist paise as int64.
func (m Money) Value() (driver.Value, error) {
	return m.paise, nil
}

// Scan implements sql.Scanner to read int64 paise from database driver.
func (m *Money) Scan(src any) error {
	if src == nil {
		m.paise = 0
		return nil
	}
	switch v := src.(type) {
	case int64:
		m.paise = v
	case int32:
		m.paise = int64(v)
	case int:
		m.paise = int64(v)
	case float64:
		m.paise = int64(math.Round(v * 100))
	case []byte:
		f, err := strconv.ParseFloat(string(v), 64)
		if err != nil {
			return err
		}
		m.paise = int64(math.Round(f * 100))
	case string:
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return err
		}
		m.paise = int64(math.Round(f * 100))
	default:
		return fmt.Errorf("unsupported type for Money: %T", src)
	}
	return nil
}
