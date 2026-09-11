package india_test

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/umesh0492/go-app-kit/india"
)

// Helper to calculate Verhoeff check digit for test generation
func generateValidAadhaar(base11 string) string {
	verhoeffD := [10][10]int{
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
	verhoeffP := [8][10]int{
		{0, 1, 2, 3, 4, 5, 6, 7, 8, 9},
		{1, 5, 7, 6, 2, 8, 3, 0, 9, 4},
		{5, 8, 0, 3, 7, 9, 6, 1, 4, 2},
		{8, 9, 1, 6, 0, 4, 3, 5, 2, 7},
		{9, 4, 5, 3, 1, 2, 6, 8, 7, 0},
		{4, 2, 8, 6, 5, 7, 3, 9, 0, 1},
		{2, 7, 9, 3, 8, 0, 6, 4, 1, 5},
		{7, 0, 4, 6, 9, 1, 3, 2, 5, 8},
	}
	verhoeffInv := [10]int{0, 4, 3, 2, 1, 5, 6, 7, 8, 9}

	c := 0
	l := len(base11)
	for i := 0; i < l; i++ {
		digit, _ := strconv.Atoi(string(base11[l-1-i]))
		c = verhoeffD[c][verhoeffP[(i+1)%8][digit]]
	}
	checkDigit := verhoeffInv[c]
	return base11 + strconv.Itoa(checkDigit)
}

func TestAadhaar(t *testing.T) {
	// Standard UIDAI Verhoeff generated test vectors
	validAadhaar := generateValidAadhaar("23456789012")
	validAadhaar2 := generateValidAadhaar("36759834601")

	t.Run("Valid Aadhaar", func(t *testing.T) {
		for _, v := range []string{validAadhaar, validAadhaar2} {
			if err := india.ValidateAadhaar(v); err != nil {
				t.Fatalf("expected valid aadhaar %s, got error: %v", v, err)
			}
			if !india.IsValidAadhaar(v) {
				t.Fatalf("expected IsValidAadhaar to return true for %s", v)
			}
		}
	})

	t.Run("Valid with spaces and dashes", func(t *testing.T) {
		spaced := validAadhaar[:4] + " " + validAadhaar[4:8] + " " + validAadhaar[8:]
		if err := india.ValidateAadhaar(spaced); err != nil {
			t.Fatalf("expected spaced aadhaar to pass, got: %v", err)
		}
		dashed := validAadhaar[:4] + "-" + validAadhaar[4:8] + "-" + validAadhaar[8:]
		if err := india.ValidateAadhaar(dashed); err != nil {
			t.Fatalf("expected dashed aadhaar to pass, got: %v", err)
		}
	})

	t.Run("Invalid Length", func(t *testing.T) {
		if err := india.ValidateAadhaar("12345"); err != india.ErrInvalidAadhaarLength {
			t.Fatalf("expected ErrInvalidAadhaarLength, got %v", err)
		}
	})

	t.Run("Invalid Prefix (0 or 1)", func(t *testing.T) {
		if err := india.ValidateAadhaar("012345678901"); err != india.ErrInvalidAadhaarPrefix {
			t.Fatalf("expected ErrInvalidAadhaarPrefix, got %v", err)
		}
		if err := india.ValidateAadhaar("112345678901"); err != india.ErrInvalidAadhaarPrefix {
			t.Fatalf("expected ErrInvalidAadhaarPrefix, got %v", err)
		}
	})

	t.Run("Non-numeric characters", func(t *testing.T) {
		if err := india.ValidateAadhaar("23456789012A"); err != india.ErrInvalidAadhaarFormat {
			t.Fatalf("expected ErrInvalidAadhaarFormat, got %v", err)
		}
	})

	t.Run("Invalid Checksum", func(t *testing.T) {
		// Flip the check digit from 9 to 0
		badChecksum := "234567890120"
		if err := india.ValidateAadhaar(badChecksum); err != india.ErrInvalidAadhaarChecksum {
			t.Fatalf("expected ErrInvalidAadhaarChecksum, got %v", err)
		}
	})

	t.Run("MaskAadhaar", func(t *testing.T) {
		masked := india.MaskAadhaar(validAadhaar)
		expectedSuffix := validAadhaar[8:]
		if masked != "XXXX-XXXX-"+expectedSuffix {
			t.Fatalf("expected XXXX-XXXX-%s, got %s", expectedSuffix, masked)
		}
		// Invalid length returns input as is
		if india.MaskAadhaar("123") != "123" {
			t.Fatalf("expected untouched for invalid length")
		}
	})

	t.Run("FormatAadhaar", func(t *testing.T) {
		formatted := india.FormatAadhaar(validAadhaar)
		expected := validAadhaar[:4] + " " + validAadhaar[4:8] + " " + validAadhaar[8:]
		if formatted != expected {
			t.Fatalf("expected %s, got %s", expected, formatted)
		}
		if india.FormatAadhaar("123") != "123" {
			t.Fatalf("expected untouched for invalid length")
		}
	})
}

func TestCurrency(t *testing.T) {
	t.Run("FormatINRPaise", func(t *testing.T) {
		tests := []struct {
			input    int64
			expected string
		}{
			{0, "0.00"},
			{50, "0.50"},
			{1995, "19.95"},
			{200, "2.00"},
			{100000, "1,000.00"},
			{1000000, "10,000.00"},
			{10000000, "1,00,000.00"},
			{123456789, "12,34,567.89"},
			{-123456789, "-12,34,567.89"},
			{-5000, "-50.00"},
			{-1995, "-19.95"},
			{1000000000, "1,00,00,000.00"}, // 1 Crore INR
		}

		for _, tc := range tests {
			actual := india.FormatINRPaise(tc.input)
			if actual != tc.expected {
				t.Errorf("FormatINRPaise(%d): expected %q, got %q", tc.input, tc.expected, actual)
			}
		}
	})

	t.Run("AmountToWordsINR", func(t *testing.T) {
		tests := []struct {
			input    int64
			expected string
		}{
			{0, "Zero Rupees Only"},
			{1, "One Rupees Only"},
			{15, "Fifteen Rupees Only"},
			{42, "Forty Two Rupees Only"},
			{105, "One Hundred Five Rupees Only"},
			{1500, "One Thousand Five Hundred Rupees Only"},
			{25000, "Twenty Five Thousand Rupees Only"},
			{100000, "One Lakh Rupees Only"},
			{5500000, "Fifty Five Lakh Rupees Only"},
			{10000000, "One Crore Rupees Only"},
			{12345678, "One Crore Twenty Three Lakh Forty Five Thousand Six Hundred Seventy Eight Rupees Only"},
			{-500, "Minus Five Hundred Rupees Only"},
			{1500000000, "One Hundred Fifty Crore Rupees Only"},
		}

		for _, tc := range tests {
			actual := india.AmountToWordsINR(tc.input)
			if actual != tc.expected {
				t.Errorf("AmountToWordsINR(%d): expected %q, got %q", tc.input, tc.expected, actual)
			}
		}
	})
}

func TestFinancialYear(t *testing.T) {
	t.Run("Q1 (April - June)", func(t *testing.T) {
		date := time.Date(2026, time.May, 15, 12, 0, 0, 0, time.UTC)
		fy := india.GetFinancialYear(date)
		if fy.Label != "FY 2026-27" || fy.ShortCode != "FY26-27" || fy.Quarter != india.Q1 {
			t.Fatalf("unexpected FY result: %+v", fy)
		}
		if fy.StartYear != 2026 || fy.EndYear != 2027 {
			t.Fatalf("unexpected years: %d-%d", fy.StartYear, fy.EndYear)
		}
	})

	t.Run("Q2 (July - September)", func(t *testing.T) {
		date := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)
		fy := india.GetFinancialYear(date)
		if fy.Quarter != india.Q2 || fy.Label != "FY 2026-27" {
			t.Fatalf("unexpected FY result: %+v", fy)
		}
	})

	t.Run("Q3 (October - December)", func(t *testing.T) {
		date := time.Date(2026, time.November, 20, 0, 0, 0, 0, time.UTC)
		fy := india.GetFinancialYear(date)
		if fy.Quarter != india.Q3 || fy.Label != "FY 2026-27" {
			t.Fatalf("unexpected FY result: %+v", fy)
		}
	})

	t.Run("Q4 (January - March)", func(t *testing.T) {
		date := time.Date(2027, time.February, 10, 0, 0, 0, 0, time.UTC)
		fy := india.GetFinancialYear(date)
		if fy.Quarter != india.Q4 || fy.Label != "FY 2026-27" {
			t.Fatalf("unexpected FY result: %+v", fy)
		}
		if fy.StartYear != 2026 || fy.EndYear != 2027 {
			t.Fatalf("unexpected years: %d-%d", fy.StartYear, fy.EndYear)
		}
	})

	t.Run("CurrentFinancialYear", func(t *testing.T) {
		fy := india.CurrentFinancialYear()
		if fy.Label == "" || fy.Quarter == "" {
			t.Fatalf("CurrentFinancialYear should not be empty: %+v", fy)
		}
	})

	t.Run("UTC Shift Prevention", func(t *testing.T) {
		// 2026-03-31 19:00:00 UTC is 2026-04-01 00:30:00 IST -> FY 2026-27 Q1
		// In naive UTC, month is March 2026 -> FY 2025-26 Q4 (wrong!)
		dateQ1 := time.Date(2026, time.March, 31, 19, 0, 0, 0, time.UTC)
		fyQ1 := india.GetFinancialYear(dateQ1)
		if fyQ1.Quarter != india.Q1 || fyQ1.Label != "FY 2026-27" {
			t.Fatalf("expected FY 2026-27 Q1 in IST, got %s %s", fyQ1.Label, fyQ1.Quarter)
		}

		// 2026-06-30 19:00:00 UTC is 2026-07-01 00:30:00 IST -> FY 2026-27 Q2
		// In naive UTC, month is June 2026 -> Q1 (wrong!)
		dateQ2 := time.Date(2026, time.June, 30, 19, 0, 0, 0, time.UTC)
		fyQ2 := india.GetFinancialYear(dateQ2)
		if fyQ2.Quarter != india.Q2 || fyQ2.Label != "FY 2026-27" {
			t.Fatalf("expected FY 2026-27 Q2 in IST, got %s %s", fyQ2.Label, fyQ2.Quarter)
		}
	})
}

func TestGSTIN(t *testing.T) {
	// Standard GSTN test vectors:
	// 27AAPFU0939F1ZV (Maharashtra, Company PAN AAPFU0939F, check digit 'V')
	// 29AAPFU0939F1ZR (Karnataka, Company PAN AAPFU0939F, check digit 'R')
	// 07AAGCA4112E1ZL (Delhi, Company PAN AAGCA4112E, check digit 'L')
	validGSTIN := "27AAPFU0939F1ZV"
	validGSTINKarnataka := "29AAPFU0939F1ZR"
	validGSTINDelhi := "07AAGCA4112E1Z9"

	t.Run("Valid GSTIN Vectors", func(t *testing.T) {
		for _, gstin := range []string{validGSTIN, validGSTINKarnataka, validGSTINDelhi} {
			if err := india.ValidateGSTIN(gstin); err != nil {
				t.Fatalf("expected valid GSTIN %s, got err: %v", gstin, err)
			}
			if !india.IsValidGSTIN(gstin) {
				t.Fatalf("expected IsValidGSTIN to be true for %s", gstin)
			}
		}

		details, err := india.ParseGSTIN(validGSTIN)
		if err != nil {
			t.Fatalf("ParseGSTIN failed: %v", err)
		}
		if details.StateCode != "27" || details.StateName != "Maharashtra" {
			t.Fatalf("unexpected state: %s, %s", details.StateCode, details.StateName)
		}
		if details.PAN != "AAPFU0939F" {
			t.Fatalf("unexpected PAN: %s", details.PAN)
		}
		if details.EntityNum != "1" || details.CheckDigit != "V" {
			t.Fatalf("unexpected entity/checkdigit: %s/%s", details.EntityNum, details.CheckDigit)
		}

		ktkDetails, err := india.ParseGSTIN(validGSTINKarnataka)
		if err != nil {
			t.Fatalf("ParseGSTIN failed on Karnataka vector: %v", err)
		}
		if ktkDetails.StateCode != "29" || ktkDetails.StateName != "Karnataka" {
			t.Fatalf("unexpected Karnataka details: %+v", ktkDetails)
		}
		if ktkDetails.CheckDigit != "R" {
			t.Fatalf("expected check digit 'R' for Karnataka vector, got %s", ktkDetails.CheckDigit)
		}
	})

	t.Run("Invalid Length", func(t *testing.T) {
		if err := india.ValidateGSTIN("27AAPFU0939F1Z"); err != india.ErrInvalidGSTINLength {
			t.Fatalf("expected ErrInvalidGSTINLength, got %v", err)
		}
	})

	t.Run("Invalid Format Regex", func(t *testing.T) {
		if err := india.ValidateGSTIN("27AAPFU0939F1X0"); err != india.ErrInvalidGSTINFormat {
			t.Fatalf("expected ErrInvalidGSTINFormat, got %v", err)
		}
	})

	t.Run("Invalid State Code", func(t *testing.T) {
		// 98 is not a valid state code
		invalidState := "98AAPFU0939F1ZV"
		if err := india.ValidateGSTIN(invalidState); err == nil {
			t.Fatalf("expected state code error")
		}
	})

	t.Run("Invalid Checksum", func(t *testing.T) {
		// Flip check digit 'V' to '0'
		badCheck := "27AAPFU0939F1Z0"
		if err := india.ValidateGSTIN(badCheck); err == nil {
			t.Fatalf("expected checksum error")
		}
	})

	t.Run("CalculateGSTINCheckDigit RFC test vectors", func(t *testing.T) {
		if check := india.CalculateGSTINCheckDigit("27AAPFU0939F1Z"); check != 'V' {
			t.Fatalf("expected check digit 'V' for 27AAPFU0939F1Z, got %c", check)
		}
		if check := india.CalculateGSTINCheckDigit("29AAPFU0939F1Z"); check != 'R' {
			t.Fatalf("expected check digit 'R' for 29AAPFU0939F1Z, got %c", check)
		}
		if check := india.CalculateGSTINCheckDigit("07AAGCA4112E1Z"); check != '9' {
			t.Fatalf("expected check digit '9' for 07AAGCA4112E1Z, got %c", check)
		}
		if india.CalculateGSTINCheckDigit("short") != '0' {
			t.Fatalf("expected '0' for short input")
		}
		if india.CalculateGSTINCheckDigit("27AAPFU0939F1!") != '0' {
			t.Fatalf("expected '0' for invalid characters")
		}
	})
}

func TestIFSC(t *testing.T) {
	// Standard RBI IFSC test vectors
	testVectors := []struct {
		ifsc       string
		bankCode   string
		branchCode string
	}{
		{"HDFC0001234", "HDFC", "001234"},
		{"SBIN0000456", "SBIN", "000456"},
		{"ICIC0000002", "ICIC", "000002"},
		{"PUNB0024000", "PUNB", "024000"},
		{"KKBK0000958", "KKBK", "000958"},
	}

	t.Run("Valid Standard IFSC Vectors", func(t *testing.T) {
		for _, tv := range testVectors {
			if err := india.ValidateIFSC(tv.ifsc); err != nil {
				t.Fatalf("expected valid IFSC %s, got: %v", tv.ifsc, err)
			}
			if !india.IsValidIFSC(tv.ifsc) {
				t.Fatalf("expected IsValidIFSC to be true for %s", tv.ifsc)
			}
			bank, err := india.GetBankCode(tv.ifsc)
			if err != nil || bank != tv.bankCode {
				t.Fatalf("unexpected bank code for %s: got %s, want %s, err: %v", tv.ifsc, bank, tv.bankCode, err)
			}
			branch, err := india.GetBranchCode(tv.ifsc)
			if err != nil || branch != tv.branchCode {
				t.Fatalf("unexpected branch code for %s: got %s, want %s, err: %v", tv.ifsc, branch, tv.branchCode, err)
			}
		}
	})

	t.Run("Invalid Length", func(t *testing.T) {
		if err := india.ValidateIFSC("HDFC001"); err != india.ErrInvalidIFSCLength {
			t.Fatalf("expected ErrInvalidIFSCLength, got %v", err)
		}
	})

	t.Run("Invalid 5th character", func(t *testing.T) {
		if err := india.ValidateIFSC("HDFC1001234"); err != india.ErrInvalidIFSCFormat {
			t.Fatalf("expected ErrInvalidIFSCFormat, got %v", err)
		}
	})

	t.Run("GetBankCode/GetBranchCode on invalid code", func(t *testing.T) {
		if _, err := india.GetBankCode("invalid"); err == nil {
			t.Fatalf("expected error from GetBankCode on invalid code")
		}
		if _, err := india.GetBranchCode("invalid"); err == nil {
			t.Fatalf("expected error from GetBranchCode on invalid code")
		}
	})
}

func TestPAN(t *testing.T) {
	// Standard ITD PAN test vectors
	companyPAN := "ABCCE1234F"
	individualPAN := "AAAPZ9876C"
	firmPAN := "AAAFR1234E"
	trustPAN := "AAATT1234C"

	t.Run("Valid Standard PAN Vectors", func(t *testing.T) {
		for _, pan := range []string{companyPAN, individualPAN, firmPAN, trustPAN} {
			if err := india.ValidatePAN(pan); err != nil {
				t.Fatalf("expected valid PAN %s, got: %v", pan, err)
			}
			if !india.IsValidPAN(pan) {
				t.Fatalf("expected IsValidPAN to be true for %s", pan)
			}
		}

		details, err := india.ParsePAN(companyPAN)
		if err != nil {
			t.Fatalf("ParsePAN failed: %v", err)
		}
		if details.EntityTypeCode != "C" || details.EntityTypeName != "Company" {
			t.Fatalf("unexpected entity type: %s, %s", details.EntityTypeCode, details.EntityTypeName)
		}
		if details.SurnameInitial != "E" {
			t.Fatalf("unexpected surname initial: %s", details.SurnameInitial)
		}

		indivDetails, err := india.ParsePAN(individualPAN)
		if err != nil {
			t.Fatalf("ParsePAN failed: %v", err)
		}
		if indivDetails.EntityTypeCode != "P" || indivDetails.EntityTypeName != "Individual (Person)" {
			t.Fatalf("unexpected individual entity: %+v", indivDetails)
		}

		firmDetails, err := india.ParsePAN(firmPAN)
		if err != nil {
			t.Fatalf("ParsePAN failed on firm: %v", err)
		}
		if firmDetails.EntityTypeCode != "F" || firmDetails.EntityTypeName != "Firm / Limited Liability Partnership (LLP)" {
			t.Fatalf("unexpected firm entity: %+v", firmDetails)
		}
	})

	t.Run("Invalid Length", func(t *testing.T) {
		if err := india.ValidatePAN("ABCDE123"); err != india.ErrInvalidPANLength {
			t.Fatalf("expected ErrInvalidPANLength, got %v", err)
		}
	})

	t.Run("Invalid Format Regex", func(t *testing.T) {
		if err := india.ValidatePAN("12345ABCDE"); err != india.ErrInvalidPANFormat {
			t.Fatalf("expected ErrInvalidPANFormat, got %v", err)
		}
	})

	t.Run("Unknown Entity Type", func(t *testing.T) {
		// 4th char 'X' is not a registered entity type
		if err := india.ValidatePAN("ABCDX1234F"); err == nil {
			t.Fatalf("expected unknown entity type error")
		}
	})
}

func TestPhone(t *testing.T) {
	t.Run("Valid Mobile Numbers", func(t *testing.T) {
		numbers := []string{
			"9876543210",
			"+919876543210",
			"+91 98765 43210",
			"919876543210",
			"09876543210",
			"8765432109",
			"7654321098",
			"6543210987",
		}

		for _, num := range numbers {
			if err := india.ValidatePhone(num); err != nil {
				t.Errorf("expected valid phone for %s, got: %v", num, err)
			}
			if !india.IsValidPhone(num) {
				t.Errorf("expected IsValidPhone to be true for %s", num)
			}
		}
	})

	t.Run("Invalid Numbers", func(t *testing.T) {
		invalid := []string{
			"5876543210",  // starts with 5
			"987654321",   // 9 digits
			"98765432100", // 11 digits without 0
			"abcdefghij",
		}

		for _, num := range invalid {
			if err := india.ValidatePhone(num); err != india.ErrInvalidPhoneFormat {
				t.Errorf("expected ErrInvalidPhoneFormat for %s, got: %v", num, err)
			}
		}
	})

	t.Run("FormatE164 & FormatNational", func(t *testing.T) {
		e164, err := india.FormatE164("09876543210")
		if err != nil || e164 != "+919876543210" {
			t.Fatalf("FormatE164 failed: %s, %v", e164, err)
		}

		nat, err := india.FormatNational("+91 98765 43210")
		if err != nil || nat != "98765-43210" {
			t.Fatalf("FormatNational failed: %s, %v", nat, err)
		}

		if _, err := india.FormatE164("123"); err == nil {
			t.Fatalf("expected error on invalid phone")
		}
		if _, err := india.FormatNational("123"); err == nil {
			t.Fatalf("expected error on invalid phone")
		}
	})
}

func TestMoney(t *testing.T) {
	t.Run("Constructors & Basic Getters", func(t *testing.T) {
		m1 := india.NewMoney(150050)
		if m1.Paise() != 150050 || m1.Rupees() != 1500 || m1.Float64() != 1500.50 {
			t.Fatalf("unexpected m1 values: paise=%d, rupees=%d, float=%.2f", m1.Paise(), m1.Rupees(), m1.Float64())
		}

		m2 := india.NewMoneyFromRupees(500)
		if m2.Paise() != 50000 || m2.Rupees() != 500 {
			t.Fatalf("unexpected m2 from rupees: %d", m2.Paise())
		}

		// Float rounding checks
		m3 := india.NewMoneyFromFloat(1.995)
		if m3.Paise() != 200 {
			t.Fatalf("expected 1.995 to round to 200 paise, got %d", m3.Paise())
		}

		m4 := india.NewMoneyFromFloat(1234.564)
		if m4.Paise() != 123456 {
			t.Fatalf("expected 1234.564 to round to 123456 paise, got %d", m4.Paise())
		}
	})

	t.Run("Predicates, Abs, Negate", func(t *testing.T) {
		zero := india.NewMoney(0)
		pos := india.NewMoney(100)
		neg := india.NewMoney(-100)

		if !zero.IsZero() || zero.IsPositive() || zero.IsNegative() {
			t.Fatalf("zero predicate failed")
		}
		if pos.IsZero() || !pos.IsPositive() || pos.IsNegative() {
			t.Fatalf("pos predicate failed")
		}
		if neg.IsZero() || neg.IsPositive() || !neg.IsNegative() {
			t.Fatalf("neg predicate failed")
		}

		if neg.Abs().Paise() != 100 || pos.Abs().Paise() != 100 {
			t.Fatalf("Abs failed")
		}
		if pos.Negate().Paise() != -100 || neg.Negate().Paise() != 100 {
			t.Fatalf("Negate failed")
		}
	})

	t.Run("Arithmetic Operations", func(t *testing.T) {
		m1 := india.NewMoney(100000) // ₹1000.00
		m2 := india.NewMoney(25050)  // ₹250.50

		sum := m1.Add(m2)
		if sum.Paise() != 125050 {
			t.Fatalf("Add failed: %d", sum.Paise())
		}

		diff := m1.Sub(m2)
		if diff.Paise() != 74950 {
			t.Fatalf("Sub failed: %d", diff.Paise())
		}

		mul := m2.Mul(3)
		if mul.Paise() != 75150 {
			t.Fatalf("Mul failed: %d", mul.Paise())
		}

		// 18% GST on ₹1000.00
		gstBps := m1.MulBasisPoints(1800) // 1800 bps = 18%
		if gstBps.Paise() != 18000 {
			t.Fatalf("MulBasisPoints failed: %d", gstBps.Paise())
		}

		gstPct := m1.Percentage(18.0)
		if gstPct.Paise() != 18000 {
			t.Fatalf("Percentage failed: %d", gstPct.Paise())
		}
	})

	t.Run("Exact Split Without Loss", func(t *testing.T) {
		m := india.NewMoney(100) // 100 paise split 3 ways
		parts, err := m.Split(3)
		if err != nil {
			t.Fatalf("Split failed: %v", err)
		}
		if len(parts) != 3 {
			t.Fatalf("expected 3 parts, got %d", len(parts))
		}

		var total int64
		for _, p := range parts {
			total += p.Paise()
		}
		if total != 100 {
			t.Fatalf("Split lost precision: total=%d, expected 100", total)
		}
		if parts[0].Paise() != 34 || parts[1].Paise() != 33 || parts[2].Paise() != 33 {
			t.Fatalf("unexpected split distribution: %+v", parts)
		}

		// Split negative
		mNeg := india.NewMoney(-100)
		negParts, err := mNeg.Split(3)
		if err != nil {
			t.Fatalf("Split negative failed: %v", err)
		}
		var negTotal int64
		for _, p := range negParts {
			negTotal += p.Paise()
		}
		if negTotal != -100 {
			t.Fatalf("Negative split lost precision: %d", negTotal)
		}

		// Division by zero
		if _, err := m.Split(0); err != india.ErrDivisionByZero {
			t.Fatalf("expected ErrDivisionByZero, got %v", err)
		}
	})

	t.Run("Formatting and Words", func(t *testing.T) {
		m := india.NewMoney(123456789) // ₹12,34,567.89
		if m.Format() != "12,34,567.89" {
			t.Fatalf("Format failed: %s", m.Format())
		}
		if m.String() != "12,34,567.89" {
			t.Fatalf("String failed: %s", m.String())
		}

		words := m.Words()
		if !strings.Contains(words, "Twelve Lakh Thirty Four Thousand Five Hundred Sixty Seven Rupees Only") {
			t.Fatalf("Words failed: %s", words)
		}
	})

	t.Run("JSON Serialization", func(t *testing.T) {
		type Invoice struct {
			Amount india.Money `json:"amount"`
		}

		inv := Invoice{Amount: india.NewMoney(150075)}
		data, err := json.Marshal(inv)
		if err != nil {
			t.Fatalf("Marshal failed: %v", err)
		}
		// Assert that serialization contains NO float number on the wire
		expectedJSON := `{"amount":{"amount_paise":150075,"formatted":"1,500.75","currency":"INR"}}`
		if string(data) != expectedJSON {
			t.Fatalf("unexpected JSON: %s, expected: %s", string(data), expectedJSON)
		}
		if strings.Contains(string(data), `1500.75`) {
			t.Fatalf("wire serialization must not contain float number: %s", string(data))
		}

		var parsed Invoice
		if err := json.Unmarshal(data, &parsed); err != nil {
			t.Fatalf("Unmarshal failed: %v", err)
		}
		if parsed.Amount.Paise() != 150075 {
			t.Fatalf("parsed amount mismatch: %d", parsed.Amount.Paise())
		}

		// Unmarshal integer paise representation
		intJSON := `{"amount":150075}`
		if err := json.Unmarshal([]byte(intJSON), &parsed); err != nil {
			t.Fatalf("Unmarshal integer paise failed: %v", err)
		}
		if parsed.Amount.Paise() != 150075 {
			t.Fatalf("parsed integer paise mismatch: %d", parsed.Amount.Paise())
		}

		// Unmarshal formatted string representation
		jsonStr := `{"amount":"12,34,567.89"}`
		if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
			t.Fatalf("Unmarshal formatted string failed: %v", err)
		}
		if parsed.Amount.Paise() != 123456789 {
			t.Fatalf("parsed formatted string mismatch: %d", parsed.Amount.Paise())
		}

		// Unmarshal legacy float number gracefully
		legacyFloatJSON := `{"amount":1500.75}`
		if err := json.Unmarshal([]byte(legacyFloatJSON), &parsed); err != nil {
			t.Fatalf("Unmarshal legacy float failed: %v", err)
		}
		if parsed.Amount.Paise() != 150075 {
			t.Fatalf("parsed legacy float mismatch: %d", parsed.Amount.Paise())
		}

		// Unmarshal object with paise field
		paiseJSON := `{"amount":{"paise":150075}}`
		if err := json.Unmarshal([]byte(paiseJSON), &parsed); err != nil {
			t.Fatalf("Unmarshal paise object failed: %v", err)
		}
		if parsed.Amount.Paise() != 150075 {
			t.Fatalf("parsed paise object mismatch: %d", parsed.Amount.Paise())
		}

		// Unmarshal object with formatted string
		formattedJSON := `{"amount":{"formatted":"1,500.75"}}`
		if err := json.Unmarshal([]byte(formattedJSON), &parsed); err != nil {
			t.Fatalf("Unmarshal formatted object failed: %v", err)
		}
		if parsed.Amount.Paise() != 150075 {
			t.Fatalf("parsed formatted object mismatch: %d", parsed.Amount.Paise())
		}

		// Unmarshal empty object returns error
		if err := json.Unmarshal([]byte(`{"amount":{}}`), &parsed); err == nil {
			t.Fatalf("expected error on empty object")
		}

		// Unmarshal object with invalid formatted string
		if err := json.Unmarshal([]byte(`{"amount":{"formatted":"not-a-number"}}`), &parsed); err == nil {
			t.Fatalf("expected error on invalid formatted object")
		}

		// Unmarshal object with malformed types
		if err := json.Unmarshal([]byte(`{"amount":{"amount_paise":"not-an-int"}}`), &parsed); err == nil {
			t.Fatalf("expected error on malformed amount_paise type")
		}

		// Unmarshal empty string
		var emptyMoney india.Money
		if err := emptyMoney.UnmarshalJSON([]byte("")); err != nil || emptyMoney.Paise() != 0 {
			t.Fatalf("expected empty string to unmarshal to 0")
		}

		// Unmarshal invalid string
		if err := json.Unmarshal([]byte(`{"amount":"invalid-num"}`), &parsed); err == nil {
			t.Fatalf("expected error on invalid money string")
		}

		// Unmarshal null
		if err := json.Unmarshal([]byte(`{"amount":null}`), &parsed); err != nil || parsed.Amount.Paise() != 0 {
			t.Fatalf("expected null to unmarshal to 0")
		}
	})

	t.Run("SQL Driver Scan and Value", func(t *testing.T) {
		m := india.NewMoney(4200)
		val, err := m.Value()
		if err != nil || val != int64(4200) {
			t.Fatalf("Value failed: %v, %v", val, err)
		}

		var scanned india.Money
		if err := scanned.Scan(int64(9900)); err != nil || scanned.Paise() != 9900 {
			t.Fatalf("Scan int64 failed: %v, %d", err, scanned.Paise())
		}
		if err := scanned.Scan(int32(5000)); err != nil || scanned.Paise() != 5000 {
			t.Fatalf("Scan int32 failed: %v", err)
		}
		if err := scanned.Scan(int(3000)); err != nil || scanned.Paise() != 3000 {
			t.Fatalf("Scan int failed: %v", err)
		}
		if err := scanned.Scan(12.50); err != nil || scanned.Paise() != 1250 {
			t.Fatalf("Scan float64 failed: %v", err)
		}
		if err := scanned.Scan([]byte("99.99")); err != nil || scanned.Paise() != 9999 {
			t.Fatalf("Scan bytes failed: %v", err)
		}
		if err := scanned.Scan("45.50"); err != nil || scanned.Paise() != 4550 {
			t.Fatalf("Scan string failed: %v", err)
		}
		if err := scanned.Scan(nil); err != nil || scanned.Paise() != 0 {
			t.Fatalf("Scan nil failed: %v", err)
		}
		if err := scanned.Scan(struct{}{}); err == nil {
			t.Fatalf("expected error on unsupported type")
		}
		if err := scanned.Scan("bad-number"); err == nil {
			t.Fatalf("expected error on bad number string")
		}
		if err := scanned.Scan([]byte("bad-bytes")); err == nil {
			t.Fatalf("expected error on bad bytes string")
		}
	})
}
