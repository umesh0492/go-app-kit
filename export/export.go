package export

import "io"

// Column defines how a specific field of type T is rendered in tabular exports.
type Column[T any] struct {
	Header    string
	Extractor func(item T) string
}

// Exporter defines the contract for streaming tabular datasets.
type Exporter[T any] interface {
	WriteHeader() error
	WriteRow(item T) error
	WriteRows(items []T) error
	Flush() error
	Close() error
}

// WriterFlusher is an io.Writer that optionally supports manual buffer flushing.
type WriterFlusher interface {
	io.Writer
	Flush()
}
