package pdf_test

import (
	"errors"
	"os"
	"testing"

	wkhtml "github.com/SebastiaanKlippert/go-wkhtmltopdf"
	"github.com/umesh0492/go-app-kit/pdf"
)

type mockGenerator struct {
	addPageCalled bool
	pageReader    *wkhtml.PageReader
	createFunc    func() error
	bytesFunc     func() []byte
}

func (m *mockGenerator) AddPage(p *wkhtml.PageReader) {
	m.addPageCalled = true
	m.pageReader = p
}

func (m *mockGenerator) Create() error {
	return m.createFunc()
}

func (m *mockGenerator) Bytes() []byte {
	return m.bytesFunc()
}

func TestGenerate_Success(t *testing.T) {
	genFunc := func(opts pdf.Options) (pdf.Generator, error) {
		if opts.PageSize != "Letter" || opts.Orientation != "Landscape" || opts.DPI != 150 {
			t.Errorf("options not passed correctly: %+v", opts)
		}
		return &mockGenerator{
			createFunc: func() error { return nil },
			bytesFunc:  func() []byte { return []byte("MOCK_PDF_CONTENT") },
		}, nil
	}

	buf, err := pdf.Generate("<html><body>Test</body></html>",
		pdf.WithGeneratorFunc(genFunc),
		pdf.WithPageSize("Letter"),
		pdf.WithOrientation("Landscape"),
		pdf.WithDPI(150),
		pdf.WithMargins(15, 15, 20, 20),
		pdf.WithTitle("Test Document"),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if buf == nil || buf.String() != "MOCK_PDF_CONTENT" {
		t.Fatalf("unexpected output: %v", buf)
	}
}

func TestGenerate_NewGeneratorError(t *testing.T) {
	expectedErr := errors.New("initialization failed")
	genFunc := func(opts pdf.Options) (pdf.Generator, error) {
		return nil, expectedErr
	}

	buf, err := pdf.Generate("<html></html>", pdf.WithGeneratorFunc(genFunc))
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected error %v, got %v", expectedErr, err)
	}
	if buf != nil {
		t.Fatalf("expected nil buffer")
	}
}

func TestGenerate_CreateError(t *testing.T) {
	expectedErr := errors.New("render failed")
	mock := &mockGenerator{
		createFunc: func() error { return expectedErr },
	}

	buf, err := pdf.Generate("<html></html>", pdf.WithGenerator(mock))
	if err == nil {
		t.Fatalf("expected error from Create()")
	}
	if buf != nil {
		t.Fatalf("expected nil buffer")
	}
}

func TestGenerate_LocalFileAccess(t *testing.T) {
	t.Run("Default disables local file access", func(t *testing.T) {
		mock := &mockGenerator{
			createFunc: func() error { return nil },
			bytesFunc:  func() []byte { return []byte("PDF") },
		}
		_, err := pdf.Generate("<html></html>", pdf.WithGenerator(mock))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if mock.pageReader == nil {
			t.Fatalf("expected pageReader to be captured")
		}
		hasLocalAccess := false
		for _, arg := range mock.pageReader.Args() {
			if arg == "--enable-local-file-access" {
				hasLocalAccess = true
				break
			}
		}
		if hasLocalAccess {
			t.Errorf("expected EnableLocalFileAccess to be false by default, got true")
		}
	})

	t.Run("WithLocalFileAccess(true) enables local file access", func(t *testing.T) {
		mock := &mockGenerator{
			createFunc: func() error { return nil },
			bytesFunc:  func() []byte { return []byte("PDF") },
		}
		_, err := pdf.Generate("<html></html>", pdf.WithGenerator(mock), pdf.WithLocalFileAccess(true))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if mock.pageReader == nil {
			t.Fatalf("expected pageReader to be captured")
		}
		hasLocalAccess := false
		for _, arg := range mock.pageReader.Args() {
			if arg == "--enable-local-file-access" {
				hasLocalAccess = true
				break
			}
		}
		if !hasLocalAccess {
			t.Errorf("expected EnableLocalFileAccess to be true when explicitly enabled, got false")
		}
	})
}

func TestRenderTemplate(t *testing.T) {
	tmpl := "Hello, {{.Name | upper}}! Your role is {{.Role | lower}}."
	data := map[string]string{
		"Name": "John",
		"Role": "ADMIN",
	}

	rendered, err := pdf.RenderTemplate(tmpl, data)
	if err != nil {
		t.Fatalf("RenderTemplate failed: %v", err)
	}
	expected := "Hello, JOHN! Your role is admin."
	if rendered != expected {
		t.Fatalf("expected %q, got %q", expected, rendered)
	}

	// Bad template syntax
	if _, err := pdf.RenderTemplate("{{.Unclosed", data); err == nil {
		t.Fatalf("expected syntax error")
	}

	// Execution error (missing field in strict or invalid op)
	if _, err := pdf.RenderTemplate("{{len .Missing}}", data); err == nil {
		t.Fatalf("expected execution error")
	}
}

func TestGenerateFromTemplate(t *testing.T) {
	mock := &mockGenerator{
		createFunc: func() error { return nil },
		bytesFunc:  func() []byte { return []byte("PDF_FROM_TEMPLATE") },
	}

	tmpl := "<h1>{{.Title}}</h1>"
	buf, err := pdf.GenerateFromTemplate(tmpl, map[string]string{"Title": "Invoice"}, pdf.WithGenerator(mock))
	if err != nil {
		t.Fatalf("GenerateFromTemplate failed: %v", err)
	}
	if buf.String() != "PDF_FROM_TEMPLATE" {
		t.Fatalf("unexpected content: %s", buf.String())
	}

	// Failed template
	if _, err := pdf.GenerateFromTemplate("{{.Bad", nil, pdf.WithGenerator(mock)); err == nil {
		t.Fatalf("expected error for bad template")
	}
}

func TestGSTInvoiceTemplate_Render(t *testing.T) {
	if len(pdf.GSTInvoiceTemplate) == 0 {
		t.Fatalf("GSTInvoiceTemplate is empty")
	}

	data := map[string]any{
		"InvoiceNumber": "INV-2026-001",
		"InvoiceDate":   "01-Apr-2026",
		"DueDate":       "15-Apr-2026",
		"PlaceOfSupply": "Maharashtra (27)",
		"ReverseCharge": false,
		"IsInterState":  false,
		"PONumber":      "PO-9988",
		"PODate":        "28-Mar-2026",
		"PaymentTerms":  "Net 15",
		"FinancialYear": "FY 2026-27",
		"Supplier": map[string]string{
			"Name":      "Acme Cloud Technologies Pvt Ltd",
			"Address":   "Bandra Kurla Complex, Mumbai",
			"GSTIN":     "27AAAPZ1234F1Z5",
			"StateName": "Maharashtra",
			"StateCode": "27",
			"PAN":       "AAAPZ1234F",
			"Email":     "billing@acmecloud.in",
		},
		"Customer": map[string]string{
			"Name":      "Bharat Retail Solutions Ltd",
			"Address":   "Koramangala, Bengaluru",
			"GSTIN":     "29AAAPZ5678F1Z9",
			"StateName": "Karnataka",
			"StateCode": "29",
			"PAN":       "AAAPZ5678F",
		},
		"Items": []map[string]any{
			{
				"Index":         1,
				"Description":   "Cloud Infrastructure Hosting",
				"HSN":           "998313",
				"Quantity":      1,
				"UnitPrice":     "50,000.00",
				"TaxableAmount": "50,000.00",
				"CGSTRate":      9,
				"CGSTAmount":    "4,500.00",
				"SGSTRate":      9,
				"SGSTAmount":    "4,500.00",
				"TotalAmount":   "59,000.00",
			},
		},
		"SubTotal":      "50,000.00",
		"TotalCGST":     "4,500.00",
		"TotalSGST":     "4,500.00",
		"GrandTotal":    "59,000.00",
		"AmountInWords": "Fifty Nine Thousand Rupees Only",
		"BankDetails": map[string]string{
			"BankName":      "HDFC Bank",
			"AccountNumber": "50200012345678",
			"IFSC":          "HDFC0000001",
			"Branch":        "BKC Mumbai",
		},
	}

	rendered, err := pdf.RenderTemplate(pdf.GSTInvoiceTemplate, data)
	if err != nil {
		t.Fatalf("failed to render GSTInvoiceTemplate: %v", err)
	}
	if len(rendered) < 100 {
		t.Fatalf("rendered output too small: %s", rendered)
	}
}

func TestReceiptTemplate_Render(t *testing.T) {
	if len(pdf.ReceiptTemplate) == 0 {
		t.Fatalf("ReceiptTemplate is empty")
	}

	data := map[string]any{
		"ReceiptNumber":        "REC-1002",
		"Amount":               "15,000.00",
		"AmountInWords":        "Fifteen Thousand Rupees Only",
		"PaymentDate":          "05-Apr-2026",
		"PaymentMode":          "UPI / NetBanking",
		"TransactionRef":       "UPI-309812739182",
		"CustomerName":         "Rohan Sharma",
		"CustomerEmail":        "rohan@example.com",
		"InvoiceReference":     "INV-2026-001",
		"Status":               "SUCCESS",
		"MerchantName":         "Acme Cloud Technologies",
		"Notes":                "Subscription renewal for Q1 2026",
		"MerchantSupportEmail": "support@acmecloud.in",
	}

	rendered, err := pdf.RenderTemplate(pdf.ReceiptTemplate, data)
	if err != nil {
		t.Fatalf("failed to render ReceiptTemplate: %v", err)
	}
	if len(rendered) < 100 {
		t.Fatalf("rendered output too small: %s", rendered)
	}
}

func TestWkhtmlGenerator_Methods(t *testing.T) {
	raw := &wkhtml.PDFGenerator{}
	g := pdf.NewWkhtmlGenerator(raw)

	g.AddPage(&wkhtml.PageReader{})
	b := g.Bytes()
	if b != nil {
		t.Fatalf("expected nil bytes before create")
	}

	// Calling Create() without wkhtmltopdf installed will return an error, which is expected
	err := g.Create()
	if err == nil {
		t.Logf("wkhtmltopdf binary present on host")
	}
}

func TestDefaultNewGenerator(t *testing.T) {
	// With valid/default options
	g, err := pdf.NewGenerator(pdf.DefaultOptions())
	if err != nil {
		t.Logf("NewGenerator returned error without wkhtmltopdf in path: %v", err)
		return
	}
	if g == nil {
		t.Fatalf("expected non-nil generator")
	}
}

func TestDefaultNewGenerator_Error(t *testing.T) {
	origEnv := os.Getenv("WKHTMLTOPDF_PATH")
	os.Setenv("WKHTMLTOPDF_PATH", "/nonexistent_binary_location")
	defer func() {
		os.Setenv("WKHTMLTOPDF_PATH", origEnv)
		wkhtml.SetPath("")
	}()
	wkhtml.SetPath("")

	g, err := pdf.NewGenerator(pdf.DefaultOptions())
	if err == nil {
		t.Fatalf("expected error with nonexistent WKHTMLTOPDF_PATH")
	}
	if g != nil {
		t.Fatalf("expected nil generator on error")
	}
}
