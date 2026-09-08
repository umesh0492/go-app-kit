package pdf

import (
	_ "embed"
)

// GSTInvoiceTemplate contains the default Indian GST-compliant tax invoice HTML template.
//
//go:embed templates/gst_invoice.html
var GSTInvoiceTemplate string

// ReceiptTemplate contains the default payment receipt HTML template.
//
//go:embed templates/receipt.html
var ReceiptTemplate string
