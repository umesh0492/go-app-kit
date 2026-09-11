package main

import (
	"context"
	"fmt"
	"time"

	"github.com/umesh0492/go-app-kit/audit"
	"github.com/umesh0492/go-app-kit/india"
	"github.com/umesh0492/go-app-kit/notifications"
	"github.com/umesh0492/go-app-kit/outbox"
	"github.com/umesh0492/go-app-kit/pdf"
)

// InvoiceItem models an individual invoice line item with exact monetary precision.
type InvoiceItem struct {
	Index         int         `json:"index"`
	Description   string      `json:"description"`
	HSN           string      `json:"hsn"`
	Quantity      int         `json:"quantity"`
	UnitPrice     india.Money `json:"unit_price"`
	TaxableAmount india.Money `json:"taxable_amount"`
	CGSTRate      float64     `json:"cgst_rate"`
	CGSTAmount    india.Money `json:"cgst_amount"`
	SGSTRate      float64     `json:"sgst_rate"`
	SGSTAmount    india.Money `json:"sgst_amount"`
	TotalAmount   india.Money `json:"total_amount"`
}

// InvoiceRequest contains inputs for creating a GST compliant invoice.
type InvoiceRequest struct {
	Tx            outbox.DBOperator
	InvoiceNumber string
	SupplierGSTIN string
	SupplierName  string
	BuyerGSTIN    string
	BuyerName     string
	BuyerEmail    string
	Description   string
	TaxableAmount india.Money // migrated from float64 to india.Money (integer paise precision)
	CGSTRate      float64
	SGSTRate      float64
	BankIFSC      string
	BankAccount   string
	BankName      string
	Branch        string
	Items         []InvoiceItem // optional line items with exact monetary precision
}

// InvoiceService coordinates validation, PDF generation, outbox transactional dispatch, and audit logs.
type InvoiceService struct {
	auditRecorder      audit.Recorder
	outboxStore        outbox.Store
	notificationBroker notifications.Broker
	pdfOpts            []pdf.Option
}

// NewInvoiceService initializes the invoice service.
func NewInvoiceService(ar audit.Recorder, os outbox.Store, nb notifications.Broker, pdfOpts ...pdf.Option) *InvoiceService {
	return &InvoiceService{
		auditRecorder:      ar,
		outboxStore:        os,
		notificationBroker: nb,
		pdfOpts:            pdfOpts,
	}
}

// ProcessInvoice executes end-to-end invoice creation, PDF generation, audit recording, and notifications.
// It persists the domain event to the outbox store using the provided active database transaction.
func (s *InvoiceService) ProcessInvoice(ctx context.Context, tx outbox.DBOperator, req InvoiceRequest) ([]byte, error) {
	if tx == nil && req.Tx != nil {
		tx = req.Tx
	}
	// 1. Validate Indian localized financial credentials
	if err := india.ValidateGSTIN(req.SupplierGSTIN); err != nil {
		return nil, fmt.Errorf("invalid supplier GSTIN: %w", err)
	}
	if err := india.ValidateGSTIN(req.BuyerGSTIN); err != nil {
		return nil, fmt.Errorf("invalid buyer GSTIN: %w", err)
	}
	if err := india.ValidateIFSC(req.BankIFSC); err != nil {
		return nil, fmt.Errorf("invalid bank IFSC: %w", err)
	}

	supplierDetails, _ := india.ParseGSTIN(req.SupplierGSTIN)
	buyerDetails, _ := india.ParseGSTIN(req.BuyerGSTIN)
	fy := india.CurrentFinancialYear()

	// 2. Compute Tax Amounts & Currency Words with integer paise precision
	var items []map[string]any
	var taxableMoney, cgstMoney, sgstMoney, totalMoney india.Money

	if len(req.Items) > 0 {
		for i, item := range req.Items {
			idx := item.Index
			if idx <= 0 {
				idx = i + 1
			}
			qty := item.Quantity
			if qty <= 0 {
				qty = 1
			}
			itemTaxable := item.TaxableAmount
			if itemTaxable.IsZero() && !item.UnitPrice.IsZero() {
				itemTaxable = item.UnitPrice.Mul(int64(qty))
			}
			cgstRate := item.CGSTRate
			if cgstRate == 0 && req.CGSTRate != 0 {
				cgstRate = req.CGSTRate
			}
			sgstRate := item.SGSTRate
			if sgstRate == 0 && req.SGSTRate != 0 {
				sgstRate = req.SGSTRate
			}
			itemCGST := item.CGSTAmount
			if itemCGST.IsZero() && cgstRate > 0 {
				itemCGST = itemTaxable.Percentage(cgstRate)
			}
			itemSGST := item.SGSTAmount
			if itemSGST.IsZero() && sgstRate > 0 {
				itemSGST = itemTaxable.Percentage(sgstRate)
			}
			itemTotal := item.TotalAmount
			if itemTotal.IsZero() {
				itemTotal = itemTaxable.Add(itemCGST).Add(itemSGST)
			}

			taxableMoney = taxableMoney.Add(itemTaxable)
			cgstMoney = cgstMoney.Add(itemCGST)
			sgstMoney = sgstMoney.Add(itemSGST)

			hsn := item.HSN
			if hsn == "" {
				hsn = "998313"
			}
			desc := item.Description
			if desc == "" {
				desc = req.Description
			}
			unitPrice := item.UnitPrice
			if unitPrice.IsZero() {
				unitPrice = itemTaxable
			}

			items = append(items, map[string]any{
				"Index":         idx,
				"Description":   desc,
				"HSN":           hsn,
				"Quantity":      qty,
				"UnitPrice":     unitPrice.Format(),
				"TaxableAmount": itemTaxable.Format(),
				"CGSTRate":      cgstRate,
				"CGSTAmount":    itemCGST.Format(),
				"SGSTRate":      sgstRate,
				"SGSTAmount":    itemSGST.Format(),
				"TotalAmount":   itemTotal.Format(),
			})
		}
		totalMoney = taxableMoney.Add(cgstMoney).Add(sgstMoney)
	} else {
		taxableMoney = req.TaxableAmount
		cgstMoney = taxableMoney.Percentage(req.CGSTRate)
		sgstMoney = taxableMoney.Percentage(req.SGSTRate)
		totalMoney = taxableMoney.Add(cgstMoney).Add(sgstMoney)

		items = []map[string]any{
			{
				"Index":         1,
				"Description":   req.Description,
				"HSN":           "998313",
				"Quantity":      1,
				"UnitPrice":     taxableMoney.Format(),
				"TaxableAmount": taxableMoney.Format(),
				"CGSTRate":      req.CGSTRate,
				"CGSTAmount":    cgstMoney.Format(),
				"SGSTRate":      req.SGSTRate,
				"SGSTAmount":    sgstMoney.Format(),
				"TotalAmount":   totalMoney.Format(),
			},
		}
	}

	totalInWords := totalMoney.Words()

	templateData := map[string]any{
		"InvoiceNumber": req.InvoiceNumber,
		"InvoiceDate":   time.Now().Format("02-Jan-2006"),
		"DueDate":       time.Now().Add(15 * 24 * time.Hour).Format("02-Jan-2006"),
		"PlaceOfSupply": supplierDetails.StateName,
		"ReverseCharge": false,
		"IsInterState":  false,
		"PONumber":      "PO-REF-2026",
		"PODate":        time.Now().Format("02-Jan-2006"),
		"PaymentTerms":  "Due on Receipt",
		"FinancialYear": fy.Label,
		"Supplier": map[string]string{
			"Name":      req.SupplierName,
			"Address":   "Bandra Kurla Complex, Mumbai",
			"GSTIN":     supplierDetails.GSTIN,
			"StateName": supplierDetails.StateName,
			"StateCode": supplierDetails.StateCode,
			"PAN":       supplierDetails.PAN,
			"Email":     "billing@supplier.com",
		},
		"Customer": map[string]string{
			"Name":      req.BuyerName,
			"Address":   "Cyber City, Gurugram, Haryana",
			"GSTIN":     buyerDetails.GSTIN,
			"StateName": buyerDetails.StateName,
			"StateCode": buyerDetails.StateCode,
			"PAN":       buyerDetails.PAN,
		},
		"Items":         items,
		"SubTotal":      taxableMoney.Format(),
		"TotalCGST":     cgstMoney.Format(),
		"TotalSGST":     sgstMoney.Format(),
		"GrandTotal":    totalMoney.Format(),
		"AmountInWords": totalInWords,
		"BankDetails": map[string]string{
			"BankName":      req.BankName,
			"AccountNumber": req.BankAccount,
			"IFSC":          req.BankIFSC,
			"Branch":        req.Branch,
		},
	}

	// 3. Compile PDF from Embedded Template
	pdfBuf, err := pdf.GenerateFromTemplate(pdf.GSTInvoiceTemplate, templateData, s.pdfOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to compile invoice PDF: %w", err)
	}

	// 4. Record Compliance Audit Trail
	auditEvt := audit.NewEvent(ctx, "INVOICE_GENERATED", "Invoice", req.InvoiceNumber, nil, map[string]any{
		"invoice_number": req.InvoiceNumber,
		"grand_total":    totalMoney.Format(),
		"total_paise":    totalMoney.Paise(),
		"currency":       "INR",
		"buyer_gstin":    req.BuyerGSTIN,
	})
	if err := s.auditRecorder.Record(ctx, auditEvt); err != nil {
		return nil, fmt.Errorf("audit log recording failed: %w", err)
	}

	// 5. Enqueue Domain Event to Transactional Outbox
	outboxEvt, err := outbox.NewEvent("Invoice", req.InvoiceNumber, "InvoiceIssued", map[string]any{
		"invoice_number": req.InvoiceNumber,
		"grand_total":    totalMoney.Format(),
		"total_paise":    totalMoney.Paise(),
		"currency":       "INR",
		"buyer_email":    req.BuyerEmail,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create outbox event: %w", err)
	}
	if err := s.outboxStore.Insert(ctx, tx, *outboxEvt); err != nil {
		return nil, fmt.Errorf("outbox insert failed: %w", err)
	}

	// 6. Dispatch Customer Notification with PDF Attachment
	notifMsg := notifications.Message{
		Title:      fmt.Sprintf("Invoice %s Ready", req.InvoiceNumber),
		Body:       fmt.Sprintf("Dear %s, your invoice for %s is ready. Total: ₹ %s", req.BuyerName, req.Description, totalMoney.Format()),
		Priority:   notifications.PriorityHigh,
		Recipients: []string{req.BuyerEmail},
		Channels:   []notifications.Channel{notifications.ChannelEmail},
		Attachments: []notifications.Attachment{
			{
				Filename:    fmt.Sprintf("%s.pdf", req.InvoiceNumber),
				ContentType: "application/pdf",
				Data:        pdfBuf.Bytes(),
			},
		},
	}
	_ = s.notificationBroker.SendAsync(ctx, notifMsg)

	return pdfBuf.Bytes(), nil
}

func main() {
	fmt.Println("Invoice reference service module")
}
