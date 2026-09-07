# `india`

Zero-dependency statutory validation algorithms, financial calculators, and formatting helpers tailored for Indian enterprise SaaS and fintech workflows.

---

## When to Use

- **B2B Onboarding & KYC**: Validating Vendor/Buyer GSTIN, Company/Individual PAN, and Bank IFSC codes before persisting to databases.
- **Aadhaar Identity Verification**: Validating UIDAI 12-digit Aadhaar numbers offline using the official Verhoeff Dihedral $D_5$ checksum, and generating masked privacy strings (`XXXX-XXXX-1234`) for UIDAI compliance.
- **Tax Invoices & Billing**: Generating Indian rupee strings (`12,34,567.89`), printing statutory "Amount in Words" (Lakhs and Crores) on tax invoices, and calculating Indian Financial Years (FY 2026-27) and quarters (Q1–Q4).
- **Customer Communication**: Validating and normalizing Indian mobile numbers (`+91`, `91`, or `0` prefixes) into clean E.164 format.

---

## Why It Is Written Like That

1. **Strictly Zero Dependencies**: Implements mathematical check digit algorithms directly in pure Go standard library without importing heavy third-party regex or validation libraries.
2. **True Mathematical Validation**:
   - **Aadhaar**: Implements the authentic **Verhoeff algorithm** using dihedral group $D_5$ permutation and multiplication matrices rather than simple length/regex checks.
   - **GSTIN**: Implements the official GST Council **mod-36 checksum** where odd/even positional weights (1 and 2) are quotient-remainder factored into base-36 check characters.
   - **PAN**: Enforces entity-type semantics on the 4th character (`C` for Company, `P` for Individual, `F` for Firm/LLP, etc.).
3. **Recursive Indian Numbering**: Unlike western million/billion converters, `AmountToWordsINR` follows the Indian numeral hierarchy (Units, Hundreds, Thousands, Lakhs, Crores) recursively, seamlessly supporting multi-hundred-crore amounts without array index bounds panics.
4. **Allocation-Conscious String Parsing**: Uses byte-level indexing and string builders for phone and currency formatting to minimize GC pressure on high-throughput checkout paths.

---

## Alternatives Evaluated

- **Generic Regex Validators**: Naive regexes check pattern structure (`^[0-9]{12}$`) but cannot detect digit transpositions, single-digit typos, or forged check digits.
- **External KYC REST APIs**: Making network calls to external APIs (e.g. Karza, Signzy, Razorpay) for initial syntax validation introduces 200–500ms network latency, API costs, and external points of failure.
- **Western Currency Libraries**: Standard `humanize` or `accounting` libraries use thousand-grouping (`1,234,567.89`) which violates statutory Indian accounting standards requiring lakhs/crores grouping (`12,34,567.89`).

---

## Comparison Table

| Alternative | Pros | Cons | Why We Chose `go-app-kit/india` |
|---|---|---|---|
| **Raw Regex Checks** | Minimal code footprint | Misses 90%+ of invalid numbers due to lack of checksum validation | `go-app-kit/india` validates genuine mathematical checksums (Verhoeff $D_5$ and mod-36). |
| **External KYC Verification APIs** | Verifies active registration with government databases | High latency (200–500ms), recurring API cost, network failure point | `go-app-kit/india` performs sub-microsecond offline syntax and checksum validation before making external API calls. |
| **Western Formatting Packages** | Well-known in open source | Incompatible with Indian statutory formats (Lakhs/Crores grouping) | `go-app-kit/india` complies natively with Reserve Bank of India and GST Council formatting rules. |

---

## Usage Example

```go
package main

import (
    "fmt"
    "github.com/umesh0492/go-app-kit/india"
)

func main() {
    // 1. Validate GSTIN with mod-36 checksum
    gstin := "27AAPFU0939F1ZV"
    if err := india.ValidateGSTIN(gstin); err != nil {
        panic(err)
    }
    details, _ := india.ParseGSTIN(gstin)
    fmt.Printf("State: %s (Code: %s), PAN: %s\n", details.StateName, details.StateCode, details.PAN)

    // 2. Validate Aadhaar with Verhoeff D5 algorithm
    aadhaar := "234567890128"
    if india.IsValidAadhaar(aadhaar) {
        fmt.Println("Masked Aadhaar:", india.MaskAadhaar(aadhaar)) // "XXXX-XXXX-0128"
    }

    // 3. Indian Currency Formatting & Words
    paise := int64(154200050)
    fmt.Println(india.FormatINRPaise(paise))                 // "15,42,000.50"
    money := india.NewMoney(paise)
    fmt.Println(money.Format())                             // "15,42,000.50"
    fmt.Println(india.AmountToWordsINR(money.Rupees()))     // "Fifteen Lakh Forty Two Thousand Rupees Only"
}
```
