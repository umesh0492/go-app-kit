package export

import (
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// CSVOptions configures the CSV streaming writer.
type CSVOptions struct {
	Delimiter        rune // Column separator (default: ',')
	UseCRLF          bool // True to use \r\n line endings (Windows standard)
	IncludeBOM       bool // True to prepend UTF-8 Byte Order Mark for Microsoft Excel compatibility
	FlushInterval    int  // Flush underlying buffer every N rows (default: 100)
	SanitizeFormulas bool // True to guard against CSV formula injection (default: true)
}

// CSVOption modifies CSVOptions.
type CSVOption func(*CSVOptions)

// DefaultCSVOptions returns standard settings suitable for Web downloads and Excel.
func DefaultCSVOptions() CSVOptions {
	return CSVOptions{
		Delimiter:        ',',
		UseCRLF:          true,
		IncludeBOM:       true,
		FlushInterval:    100,
		SanitizeFormulas: true,
	}
}

// WithDelimiter overrides the CSV delimiter (e.g. ';' or '\t').
func WithDelimiter(d rune) CSVOption {
	return func(o *CSVOptions) { o.Delimiter = d }
}

// WithCRLF controls whether \r\n or \n is used as the record separator.
func WithCRLF(crlf bool) CSVOption {
	return func(o *CSVOptions) { o.UseCRLF = crlf }
}

// WithBOM controls whether the UTF-8 BOM (\xEF\xBB\xBF) is written.
func WithBOM(bom bool) CSVOption {
	return func(o *CSVOptions) { o.IncludeBOM = bom }
}

// WithFlushInterval sets the row batch interval at which the writer buffer is flushed.
func WithFlushInterval(n int) CSVOption {
	return func(o *CSVOptions) { o.FlushInterval = n }
}

// WithFormulaSanitization controls whether cell and header values are sanitized against formula injection.
func WithFormulaSanitization(enabled bool) CSVOption {
	return func(o *CSVOptions) { o.SanitizeFormulas = enabled }
}

// CSVStreamer streams records directly into an io.Writer with minimal memory overhead.
type CSVStreamer[T any] struct {
	w             io.Writer
	csvWriter     *csv.Writer
	columns       []Column[T]
	options       CSVOptions
	headerWritten bool
	rowCount      int64
}

// NewCSVStreamer instantiates a streaming CSV exporter for elements of type T.
func NewCSVStreamer[T any](w io.Writer, columns []Column[T], opts ...CSVOption) *CSVStreamer[T] {
	cfg := DefaultCSVOptions()
	for _, opt := range opts {
		opt(&cfg)
	}

	cw := csv.NewWriter(w)
	cw.Comma = cfg.Delimiter
	cw.UseCRLF = cfg.UseCRLF

	return &CSVStreamer[T]{
		w:         w,
		csvWriter: cw,
		columns:   columns,
		options:   cfg,
	}
}

// WriteHeader outputs the UTF-8 BOM (if enabled) and column headers.
func (s *CSVStreamer[T]) WriteHeader() error {
	if s.headerWritten {
		return nil
	}

	if s.options.IncludeBOM {
		// UTF-8 BOM: 0xEF, 0xBB, 0xBF
		if _, err := s.w.Write([]byte{0xEF, 0xBB, 0xBF}); err != nil {
			return fmt.Errorf("failed to write UTF-8 BOM: %w", err)
		}
	}

	headers := make([]string, len(s.columns))
	for i, col := range s.columns {
		h := col.Header
		if s.options.SanitizeFormulas {
			h = SanitizeCSVCell(h)
		}
		headers[i] = h
	}

	if err := s.csvWriter.Write(headers); err != nil {
		return fmt.Errorf("failed to write CSV header: %w", err)
	}
	s.csvWriter.Flush()
	if err := s.csvWriter.Error(); err != nil {
		return fmt.Errorf("flush error writing CSV header: %w", err)
	}

	s.headerWritten = true
	return nil
}

// trimLeadingCSVWhitespace strips leading whitespace characters preceding formula triggers:
// spaces (' '), newlines ('\n'), CRLF ("\r\n"), and non-breaking spaces (NBSP '\u00a0').
// Bare '\r' (not followed by '\n') is not trimmed because it is a formula trigger character.
func trimLeadingCSVWhitespace(val string) string {
	for val != "" {
		switch {
		case val[0] == ' ' || val[0] == '\n':
			val = val[1:]
		case strings.HasPrefix(val, "\r\n"):
			val = val[2:]
		case strings.HasPrefix(val, "\u00a0"):
			val = val[len("\u00a0"):]
		default:
			return val
		}
	}
	return val
}

// SanitizeCSVCell applies formula injection protection (CWE-1236) by prefixing
// dangerous leading characters (=, +, -, @, \t, \r) with a single quote (').
// Guards against leading spaces, newlines ('\n', '\r\n'), and non-breaking spaces
// (NBSP '\u00a0') preceding formula trigger characters to eliminate whitespace evasion bypasses.
// Legitimate positive and negative numbers (e.g. -1234.50, +42, " -123.45", "\n-12.50")
// are preserved as numbers.
func SanitizeCSVCell(val string) string {
	if val == "" {
		return val
	}

	trimmed := trimLeadingCSVWhitespace(val)
	if trimmed == "" {
		return val
	}

	switch trimmed[0] {
	case '=', '@', '\t', '\r':
		return "'" + val
	case '+', '-':
		allTrimmed := strings.TrimSpace(trimmed)
		if _, err := strconv.ParseFloat(allTrimmed, 64); err == nil {
			return val
		}
		return "'" + val
	}

	return val
}

// WriteRow extracts column values from an item and writes a single CSV record.
func (s *CSVStreamer[T]) WriteRow(item T) error {
	if !s.headerWritten {
		if err := s.WriteHeader(); err != nil {
			return err
		}
	}

	row := make([]string, len(s.columns))
	for i, col := range s.columns {
		cell := col.Extractor(item)
		if s.options.SanitizeFormulas {
			cell = SanitizeCSVCell(cell)
		}
		row[i] = cell
	}

	if err := s.csvWriter.Write(row); err != nil {
		return fmt.Errorf("failed to write CSV row: %w", err)
	}

	s.rowCount++
	if s.options.FlushInterval > 0 && int(s.rowCount)%s.options.FlushInterval == 0 {
		s.csvWriter.Flush()
		if err := s.csvWriter.Error(); err != nil {
			return fmt.Errorf("flush error writing CSV row: %w", err)
		}
	}

	return nil
}

// WriteRows sequentially writes multiple records.
func (s *CSVStreamer[T]) WriteRows(items []T) error {
	for _, item := range items {
		if err := s.WriteRow(item); err != nil {
			return err
		}
	}
	return nil
}

// RowCount returns the total number of data rows written.
func (s *CSVStreamer[T]) RowCount() int64 {
	return s.rowCount
}

// Flush flushes any pending buffered data to the underlying io.Writer.
func (s *CSVStreamer[T]) Flush() error {
	s.csvWriter.Flush()
	return s.csvWriter.Error()
}

// Close flushes data to the underlying writer without closing non-owned io.Writer.
func (s *CSVStreamer[T]) Close() error {
	return s.Flush()
}
