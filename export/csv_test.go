package export_test

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/umesh0492/go-app-kit/export"
)

type InvoiceRecord struct {
	ID        string
	Customer  string
	Amount    float64
	Status    string
	GSTNumber string
}

type errWriter struct {
	failOnWrite bool
}

func (e *errWriter) Write(p []byte) (n int, err error) {
	if e.failOnWrite {
		return 0, errors.New("simulated disk write failure")
	}
	return len(p), nil
}

type mockCloserWriter struct {
	bytes.Buffer
	closed bool
}

func (m *mockCloserWriter) Close() error {
	m.closed = true
	return nil
}

func TestCSVStreamer_Basic(t *testing.T) {
	columns := []export.Column[InvoiceRecord]{
		{Header: "Invoice ID", Extractor: func(r InvoiceRecord) string { return r.ID }},
		{Header: "Customer", Extractor: func(r InvoiceRecord) string { return r.Customer }},
		{Header: "Amount", Extractor: func(r InvoiceRecord) string { return fmt.Sprintf("%.2f", r.Amount) }},
		{Header: "Status", Extractor: func(r InvoiceRecord) string { return r.Status }},
		{Header: "GSTIN", Extractor: func(r InvoiceRecord) string { return r.GSTNumber }},
	}

	var buf bytes.Buffer
	streamer := export.NewCSVStreamer(&buf, columns,
		export.WithBOM(true),
		export.WithCRLF(true),
		export.WithFlushInterval(2),
	)

	items := []InvoiceRecord{
		{ID: "INV-001", Customer: "Acme Corp", Amount: 15000.50, Status: "PAID", GSTNumber: "27AAPFU0939F1ZV"},
		{ID: "INV-002", Customer: "Beta Ltd", Amount: 2400.00, Status: "PENDING", GSTNumber: "29AAPFU0939F1ZV"},
		{ID: "INV-003", Customer: "Gamma LLC", Amount: 990.00, Status: "CANCELLED", GSTNumber: "07AAPFU0939F1ZV"},
	}

	if err := streamer.WriteRows(items); err != nil {
		t.Fatalf("WriteRows failed: %v", err)
	}
	if err := streamer.Flush(); err != nil {
		t.Fatalf("Flush failed: %v", err)
	}

	if streamer.RowCount() != 3 {
		t.Fatalf("expected 3 rows written, got %d", streamer.RowCount())
	}

	out := buf.Bytes()
	// Verify UTF-8 BOM
	if len(out) < 3 || out[0] != 0xEF || out[1] != 0xBB || out[2] != 0xBF {
		t.Fatalf("expected UTF-8 BOM at start of buffer")
	}

	content := string(out[3:])
	if !strings.Contains(content, "Invoice ID,Customer,Amount,Status,GSTIN\r\n") {
		t.Fatalf("missing or malformed header line: %s", content)
	}
	if !strings.Contains(content, "INV-001,Acme Corp,15000.50,PAID,27AAPFU0939F1ZV\r\n") {
		t.Fatalf("missing row 1: %s", content)
	}
	if !strings.Contains(content, "INV-003,Gamma LLC,990.00,CANCELLED,07AAPFU0939F1ZV\r\n") {
		t.Fatalf("missing row 3: %s", content)
	}
}

func TestCSVStreamer_CustomDelimiter(t *testing.T) {
	columns := []export.Column[InvoiceRecord]{
		{Header: "ID", Extractor: func(r InvoiceRecord) string { return r.ID }},
		{Header: "Customer", Extractor: func(r InvoiceRecord) string { return r.Customer }},
	}

	var buf bytes.Buffer
	streamer := export.NewCSVStreamer(&buf, columns,
		export.WithDelimiter(';'),
		export.WithBOM(false),
		export.WithCRLF(false),
	)

	err := streamer.WriteRow(InvoiceRecord{ID: "1", Customer: "Test"})
	if err != nil {
		t.Fatalf("WriteRow failed: %v", err)
	}
	_ = streamer.Flush()

	expected := "ID;Customer\n1;Test\n"
	if buf.String() != expected {
		t.Fatalf("expected %q, got %q", expected, buf.String())
	}
}

func TestCSVStreamer_Close(t *testing.T) {
	columns := []export.Column[InvoiceRecord]{
		{Header: "ID", Extractor: func(r InvoiceRecord) string { return r.ID }},
	}

	mockWriter := &mockCloserWriter{}
	streamer := export.NewCSVStreamer(mockWriter, columns)

	_ = streamer.WriteRow(InvoiceRecord{ID: "INV-1"})
	err := streamer.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	// Per security hardening, Close() flushes the buffer but does NOT close non-owned io.Writer
	if mockWriter.closed {
		t.Fatalf("expected underlying non-owned writer NOT to be closed")
	}
	if !strings.Contains(mockWriter.String(), "INV-1") {
		t.Fatalf("expected buffered data to be flushed on Close()")
	}
}

func TestCSVStreamer_FormulaInjectionGuard(t *testing.T) {
	// CWE-1236: sanitize cell values starting with =, +, -, @, \t, \r by prefixing with '
	// BUT preserve legitimate negative and positive numbers (e.g. -1234.50, +42.00).
	cases := []struct {
		input    string
		expected string
	}{
		{"=cmd|' /C calc'!A0", "'=cmd|' /C calc'!A0"},
		{"+1+1", "'+1+1"},
		{"-1+2", "'-1+2"},
		{"-2+3*cmd", "'-2+3*cmd"},
		{"-12.50", "-12.50"},
		{"-1234.50", "-1234.50"},
		{"+42.00", "+42.00"},
		{"-0.05", "-0.05"},
		{"@SUM(A1:B2)", "'@SUM(A1:B2)"},
		{"\tmalicious_tab", "'\tmalicious_tab"},
		{"\rmalicious_cr", "'\rmalicious_cr"},
		{" =cmd|' /C calc'!A0", "' =cmd|' /C calc'!A0"},
		{"  =1+1", "'  =1+1"},
		{"\t=cmd", "'\t=cmd"},
		{"   ", "   "},
		{" -12.50", " -12.50"},
		{"SAFE_VALUE", "SAFE_VALUE"},
		{"", ""},

		// Leading newline (\n) evasion tests
		{"\n=cmd|' /C calc'!A0", "'\n=cmd|' /C calc'!A0"},
		{"\n+1+1", "'\n+1+1"},
		{"\n-1+2", "'\n-1+2"},
		{"\n@SUM(A1:B2)", "'\n@SUM(A1:B2)"},
		{"\n\tmalicious_tab", "'\n\tmalicious_tab"},
		{"\n\rmalicious_cr", "'\n\rmalicious_cr"},
		{"\n-12.50", "\n-12.50"},
		{"\n+42.00", "\n+42.00"},
		{"\n", "\n"},
		{"\nSAFE_VALUE", "\nSAFE_VALUE"},

		// Leading CRLF (\r\n) evasion tests
		{"\r\n=cmd|' /C calc'!A0", "'\r\n=cmd|' /C calc'!A0"},
		{"\r\n+1+1", "'\r\n+1+1"},
		{"\r\n-1+2", "'\r\n-1+2"},
		{"\r\n@SUM(A1:B2)", "'\r\n@SUM(A1:B2)"},
		{"\r\n\tmalicious_tab", "'\r\n\tmalicious_tab"},
		{"\r\n\rmalicious_cr", "'\r\n\rmalicious_cr"},
		{"\r\n-12.50", "\r\n-12.50"},
		{"\r\n+42.00", "\r\n+42.00"},
		{"\r\n", "\r\n"},
		{"\r\nSAFE_VALUE", "\r\nSAFE_VALUE"},

		// Leading Non-Breaking Space (NBSP \u00a0) evasion tests
		{"\u00a0=cmd|' /C calc'!A0", "'\u00a0=cmd|' /C calc'!A0"},
		{"\u00a0+1+1", "'\u00a0+1+1"},
		{"\u00a0-1+2", "'\u00a0-1+2"},
		{"\u00a0@SUM(A1:B2)", "'\u00a0@SUM(A1:B2)"},
		{"\u00a0\tmalicious_tab", "'\u00a0\tmalicious_tab"},
		{"\u00a0\rmalicious_cr", "'\u00a0\rmalicious_cr"},
		{"\u00a0-12.50", "\u00a0-12.50"},
		{"\u00a0+42.00", "\u00a0+42.00"},
		{"\u00a0", "\u00a0"},
		{"\u00a0SAFE_VALUE", "\u00a0SAFE_VALUE"},

		// Mixed leading whitespace evasion tests
		{" \n\u00a0=cmd", "' \n\u00a0=cmd"},
		{"\r\n \u00a0-1234.50", "\r\n \u00a0-1234.50"},
	}

	for _, tc := range cases {
		sanitized := export.SanitizeCSVCell(tc.input)
		if sanitized != tc.expected {
			t.Errorf("SanitizeCSVCell(%q) = %q, expected %q", tc.input, sanitized, tc.expected)
		}
	}

	// Test full row streaming sanitization with legitimate numbers and malicious formulas
	columns := []export.Column[InvoiceRecord]{
		{Header: "FormulaCell", Extractor: func(r InvoiceRecord) string { return r.Customer }},
		{Header: "AmountCell", Extractor: func(r InvoiceRecord) string { return fmt.Sprintf("%.2f", r.Amount) }},
	}

	var buf bytes.Buffer
	streamer := export.NewCSVStreamer(&buf, columns, export.WithBOM(false), export.WithCRLF(false))

	_ = streamer.WriteRow(InvoiceRecord{Customer: "=2+5", Amount: -1234.50})
	_ = streamer.WriteRow(InvoiceRecord{Customer: "@SUM(1,2)", Amount: 500.00})
	_ = streamer.Flush()

	out := buf.String()
	if !strings.Contains(out, "'=2+5") {
		t.Errorf("expected '=2+5 to be sanitized in CSV output: %s", out)
	}
	if !strings.Contains(out, "'@SUM(1,2)") {
		t.Errorf("expected '@SUM(1,2) to be sanitized in CSV output: %s", out)
	}
	// Legitimate negative amount must NOT be corrupted
	if !strings.Contains(out, "-1234.50") || strings.Contains(out, "'-1234.50") {
		t.Errorf("expected -1234.50 NOT to be corrupted with leading quote: %s", out)
	}

	// Test header cell sanitization
	headerCols := []export.Column[InvoiceRecord]{
		{Header: "=CMD()", Extractor: func(r InvoiceRecord) string { return r.ID }},
	}
	var headerBuf bytes.Buffer
	headerStreamer := export.NewCSVStreamer(&headerBuf, headerCols, export.WithBOM(false), export.WithCRLF(false))
	_ = headerStreamer.WriteHeader()
	if !strings.Contains(headerBuf.String(), "'=CMD()") {
		t.Errorf("expected header to be sanitized: %s", headerBuf.String())
	}

	// Test opt-out via WithFormulaSanitization(false)
	var optOutBuf bytes.Buffer
	optOutStreamer := export.NewCSVStreamer(&optOutBuf, headerCols,
		export.WithBOM(false),
		export.WithCRLF(false),
		export.WithFormulaSanitization(false),
	)
	_ = optOutStreamer.WriteRow(InvoiceRecord{ID: "=RAW_FORMULA"})
	_ = optOutStreamer.Flush()
	optOutStr := optOutBuf.String()
	if strings.Contains(optOutStr, "'=CMD()") || strings.Contains(optOutStr, "'=RAW_FORMULA") {
		t.Errorf("expected formulas NOT to be sanitized when opt-out enabled: %s", optOutStr)
	}
}

func TestCSVStreamer_ErrorHandling(t *testing.T) {
	columns := []export.Column[InvoiceRecord]{
		{Header: "ID", Extractor: func(r InvoiceRecord) string { return r.ID }},
	}

	badWriter := &errWriter{failOnWrite: true}
	streamer := export.NewCSVStreamer(badWriter, columns, export.WithBOM(true))

	err := streamer.WriteHeader()
	if err == nil {
		t.Fatalf("expected error on bad writer WriteHeader")
	}

	// WriteRow on failed writer
	badWriter2 := &errWriter{failOnWrite: true}
	streamer2 := export.NewCSVStreamer(badWriter2, columns, export.WithBOM(false))
	err = streamer2.WriteRow(InvoiceRecord{ID: "1"})
	if err == nil {
		t.Fatalf("expected error on bad writer WriteRow")
	}
}
