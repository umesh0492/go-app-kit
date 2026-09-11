package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	wkhtml "github.com/SebastiaanKlippert/go-wkhtmltopdf"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/umesh0492/go-app-kit/audit"
	"github.com/umesh0492/go-app-kit/india"
	"github.com/umesh0492/go-app-kit/notifications"
	"github.com/umesh0492/go-app-kit/outbox"
	"github.com/umesh0492/go-app-kit/pdf"
)

type mockAuditRecorder struct {
	events []audit.Event
}

func (m *mockAuditRecorder) Record(ctx context.Context, event audit.Event) error {
	m.events = append(m.events, event)
	return nil
}

func (m *mockAuditRecorder) RecordAsync(event audit.Event) error {
	m.events = append(m.events, event)
	return nil
}

func (m *mockAuditRecorder) Close() {}

type mockOutboxStore struct {
	events []outbox.Event
	lastTx outbox.DBOperator
}

func (m *mockOutboxStore) Insert(ctx context.Context, op outbox.DBOperator, event outbox.Event) error {
	m.lastTx = op
	m.events = append(m.events, event)
	return nil
}

type mockTxOperator struct{}

func (m *mockTxOperator) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return pgconn.NewCommandTag("OK"), nil
}
func (m *mockTxOperator) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return nil, nil
}
func (m *mockTxOperator) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return nil
}

func (m *mockOutboxStore) FetchPendingBatch(ctx context.Context, limit int) ([]outbox.Event, error) {
	return m.events, nil
}

func (m *mockOutboxStore) MarkPublished(ctx context.Context, id uuid.UUID, leaseToken ...uuid.UUID) error {
	return nil
}

func (m *mockOutboxStore) MarkFailed(ctx context.Context, id uuid.UUID, lastErr string, nextRetry time.Time, finalFail bool, leaseToken ...uuid.UUID) error {
	return nil
}

type mockNotificationBroker struct {
	sentMessages []notifications.Message
}

func (m *mockNotificationBroker) RegisterSender(sender notifications.Sender) {}

func (m *mockNotificationBroker) Send(ctx context.Context, msg notifications.Message) error {
	m.sentMessages = append(m.sentMessages, msg)
	return nil
}

func (m *mockNotificationBroker) SendAsync(ctx context.Context, msg notifications.Message) error {
	m.sentMessages = append(m.sentMessages, msg)
	return nil
}

func (m *mockNotificationBroker) Close() {}

func TestProcessInvoice_EndToEnd(t *testing.T) {
	ar := &mockAuditRecorder{}
	os := &mockOutboxStore{}
	nb := &mockNotificationBroker{}

	svc := NewInvoiceService(ar, os, nb, pdf.WithGenerator(&mockGeneratorImpl{}))

	suppBase := "27AAPFU0939F1Z"
	suppCheck := india.CalculateGSTINCheckDigit(suppBase)
	supplierGSTIN := suppBase + string(suppCheck)

	buyerBase := "29AAPFU0939F1Z"
	buyerCheck := india.CalculateGSTINCheckDigit(buyerBase)
	buyerGSTIN := buyerBase + string(buyerCheck)

	req := InvoiceRequest{
		InvoiceNumber: "INV-2026-9001",
		SupplierGSTIN: supplierGSTIN,
		SupplierName:  "Bharat Cloud Tech LLP",
		BuyerGSTIN:    buyerGSTIN,
		BuyerName:     "Deccan Enterprises Ltd",
		BuyerEmail:    "accounts@deccan.in",
		Description:   "Kubernetes Architecture Consulting",
		TaxableAmount: india.NewMoneyFromRupees(100000),
		CGSTRate:      9.0,
		SGSTRate:      9.0,
		BankIFSC:      "HDFC0001234",
		BankAccount:   "50200098765432",
		BankName:      "HDFC Bank",
		Branch:        "BKC, Mumbai",
	}

	var mockTx outbox.DBOperator = &mockTxOperator{}
	pdfData, err := svc.ProcessInvoice(context.Background(), mockTx, req)
	if err != nil {
		t.Fatalf("ProcessInvoice failed: %v", err)
	}
	if len(pdfData) == 0 {
		t.Fatalf("expected non-empty PDF bytes")
	}

	// Verify Audit Record
	if len(ar.events) != 1 {
		t.Fatalf("expected 1 audit event, got %d", len(ar.events))
	}
	if ar.events[0].Action != "INVOICE_GENERATED" || ar.events[0].EntityID != "INV-2026-9001" {
		t.Fatalf("unexpected audit log: %+v", ar.events[0])
	}
	if paise, ok := ar.events[0].AfterState["total_paise"].(int64); !ok || paise != 11800000 {
		t.Fatalf("expected total_paise 11800000, got %v", ar.events[0].AfterState["total_paise"])
	}
	if grandTotal, ok := ar.events[0].AfterState["grand_total"].(string); !ok || grandTotal != "1,18,000.00" {
		t.Fatalf("expected grand_total formatted string '1,18,000.00', got %v", ar.events[0].AfterState["grand_total"])
	}
	if _, ok := ar.events[0].AfterState["grand_total"].(float64); ok {
		t.Fatalf("audit event grand_total must not be float64")
	}

	// Verify Outbox Event
	if len(os.events) != 1 {
		t.Fatalf("expected 1 outbox event, got %d", len(os.events))
	}
	if os.events[0].EventType != "InvoiceIssued" || os.events[0].AggregateID != "INV-2026-9001" {
		t.Fatalf("unexpected outbox event: %+v", os.events[0])
	}
	if os.lastTx != mockTx {
		t.Fatalf("expected active tx to be passed to outboxStore.Insert, got %v", os.lastTx)
	}
	var outboxPayload map[string]any
	if err := json.Unmarshal(os.events[0].Payload, &outboxPayload); err != nil {
		t.Fatalf("failed to unmarshal outbox payload: %v", err)
	}
	if gt, ok := outboxPayload["grand_total"].(string); !ok || gt != "1,18,000.00" {
		t.Fatalf("expected outbox grand_total string '1,18,000.00', got %v", outboxPayload["grand_total"])
	}
	if _, ok := outboxPayload["grand_total"].(float64); ok {
		t.Fatalf("outbox event grand_total must not be float64")
	}
	if paise, ok := outboxPayload["total_paise"].(float64); !ok || int64(paise) != 11800000 {
		t.Fatalf("expected outbox total_paise 11800000, got %v", outboxPayload["total_paise"])
	}

	// Verify Notification Message
	if len(nb.sentMessages) != 1 {
		t.Fatalf("expected 1 notification message, got %d", len(nb.sentMessages))
	}
	if len(nb.sentMessages[0].Attachments) != 1 {
		t.Fatalf("expected invoice attachment in notification")
	}
}

func TestProcessInvoice_MultiItemMoney(t *testing.T) {
	ar := &mockAuditRecorder{}
	os := &mockOutboxStore{}
	nb := &mockNotificationBroker{}

	svc := NewInvoiceService(ar, os, nb, pdf.WithGenerator(&mockGeneratorImpl{}))

	suppBase := "27AAPFU0939F1Z"
	suppCheck := india.CalculateGSTINCheckDigit(suppBase)
	supplierGSTIN := suppBase + string(suppCheck)

	buyerBase := "29AAPFU0939F1Z"
	buyerCheck := india.CalculateGSTINCheckDigit(buyerBase)
	buyerGSTIN := buyerBase + string(buyerCheck)

	item1 := InvoiceItem{
		Index:         1,
		Description:   "Frontend Design Tokens",
		Quantity:      2,
		UnitPrice:     india.NewMoneyFromRupees(25000),
		TaxableAmount: india.NewMoneyFromRupees(50000),
		CGSTRate:      9.0,
		SGSTRate:      9.0,
	}
	item2 := InvoiceItem{
		Index:         2,
		Description:   "Outbox Relay Architecture",
		Quantity:      1,
		UnitPrice:     india.NewMoneyFromRupees(50000),
		TaxableAmount: india.NewMoneyFromRupees(50000),
		CGSTRate:      9.0,
		SGSTRate:      9.0,
	}

	req := InvoiceRequest{
		InvoiceNumber: "INV-2026-MULTI",
		SupplierGSTIN: supplierGSTIN,
		SupplierName:  "Bharat Cloud Tech LLP",
		BuyerGSTIN:    buyerGSTIN,
		BuyerName:     "Deccan Enterprises Ltd",
		BuyerEmail:    "accounts@deccan.in",
		Description:   "Engineering Services",
		BankIFSC:      "HDFC0001234",
		BankAccount:   "50200098765432",
		BankName:      "HDFC Bank",
		Branch:        "BKC, Mumbai",
		Items:         []InvoiceItem{item1, item2},
	}

	var mockTx outbox.DBOperator = &mockTxOperator{}
	pdfData, err := svc.ProcessInvoice(context.Background(), mockTx, req)
	if err != nil {
		t.Fatalf("ProcessInvoice multi-item failed: %v", err)
	}
	if len(pdfData) == 0 {
		t.Fatalf("expected non-empty PDF bytes")
	}

	if len(ar.events) != 1 {
		t.Fatalf("expected 1 audit event, got %d", len(ar.events))
	}
	if paise, ok := ar.events[0].AfterState["total_paise"].(int64); ok && paise != 11800000 {
		t.Fatalf("expected total_paise 11800000 for multi-item, got %v", paise)
	}
}

func TestProcessInvoice_ValidationErrors(t *testing.T) {
	svc := NewInvoiceService(&mockAuditRecorder{}, &mockOutboxStore{}, &mockNotificationBroker{})

	// Invalid Supplier GSTIN
	_, err := svc.ProcessInvoice(context.Background(), nil, InvoiceRequest{
		SupplierGSTIN: "INVALID_GST",
	})
	if err == nil {
		t.Fatalf("expected error on invalid supplier GSTIN")
	}

	suppBase := "27AAPFU0939F1Z"
	suppCheck := india.CalculateGSTINCheckDigit(suppBase)
	supplierGSTIN := suppBase + string(suppCheck)

	// Invalid Buyer GSTIN
	_, err = svc.ProcessInvoice(context.Background(), nil, InvoiceRequest{
		SupplierGSTIN: supplierGSTIN,
		BuyerGSTIN:    "INVALID_BUYER_GST",
	})
	if err == nil {
		t.Fatalf("expected error on invalid buyer GSTIN")
	}

	buyerBase := "29AAPFU0939F1Z"
	buyerCheck := india.CalculateGSTINCheckDigit(buyerBase)
	buyerGSTIN := buyerBase + string(buyerCheck)

	// Invalid Bank IFSC
	_, err = svc.ProcessInvoice(context.Background(), nil, InvoiceRequest{
		SupplierGSTIN: supplierGSTIN,
		BuyerGSTIN:    buyerGSTIN,
		BankIFSC:      "INVALID_IFSC",
	})
	if err == nil {
		t.Fatalf("expected error on invalid bank IFSC")
	}
}

type mockGeneratorImpl struct{}

func (m *mockGeneratorImpl) AddPage(p *wkhtml.PageReader) {}
func (m *mockGeneratorImpl) Create() error                { return nil }
func (m *mockGeneratorImpl) Bytes() []byte                { return []byte("%PDF-1.4 Mock Invoice") }
