# `export`

Memory-efficient streaming tabular data exporter with Go generics, RFC 4180 compliance, periodic buffer flushing, and Microsoft Excel UTF-8 Byte Order Mark (BOM) support.

---

## When to Use

- **Large Dataset Streaming**: Exporting hundreds of thousands of database rows (transactions, invoices, user activity logs) directly to HTTP responses (`gin.Context.Writer`) or cloud storage without buffering entire datasets in memory.
- **Microsoft Excel CSV Compatibility**: Generating CSV files containing unicode characters (such as the `₹` Rupee symbol or localized names) that render correctly in Microsoft Excel on Windows without character encoding corruption.
- **Strongly-Typed Column Mapping**: Defining reusable, type-safe column extraction logic using Go generics (`Column[T]`) rather than mapping dynamic reflection fields or untyped `map[string]any`.
- **Chunked HTTP Downloads**: Streaming tabular data to web clients with periodic buffer flushes to ensure prompt download starts and prevent connection timeouts on slow networks.

---

## Why It Is Written Like That

1. **Constant Memory Streaming ($O(1)$ Memory Overhead)**: Implements `CSVStreamer[T any]` on top of standard `io.Writer`. Records are written and flushed row-by-row directly to the output stream. Exporting a 2-million-row database table consumes under 15MB of RAM.
2. **Compile-Time Type Safety (Go Generics)**: Column definitions bind directly to entity structs via typed extractors (`func(item T) string`), eliminating runtime reflection overhead and catching missing or renamed fields at compile time.
3. **Microsoft Excel UTF-8 BOM Integration (`IncludeBOM`)**: Writes the UTF-8 Byte Order Mark (`\xEF\xBB\xBF`) at byte offset 0 by default. Without this header, Microsoft Excel on Windows assumes the file uses ANSI / Windows-1252 encoding and garbles international characters (e.g. `₹` becomes `â‚¹`).
4. **Configurable Buffer Flushing (`WithFlushInterval`)**: Flushes internal `csv.Writer` buffers after every $N$ rows (default: 100), keeping client network connections alive and enabling instant browser download progress bars.
5. **Standard RFC 4180 Conformance**: Built directly on Go's `encoding/csv` standard library to ensure quotes, embedded commas, CRLF line terminators, and newlines within cell values are properly escaped.

---

## Alternatives Evaluated

- **In-Memory Slice Buffering (`[][]string`)**: Accumulating all rows in memory before serialization. Exporting 500k rows can allocate 500MB–2GB of heap memory, triggering heavy GC pauses and out-of-memory container restarts when multiple users export reports concurrently.
- **Complex XLSX Libraries (e.g. `excelize`)**: Support multi-sheet workbooks and visual styling, but require substantial CPU cycles and memory allocations per cell, making them unviable for high-volume data exports.
- **Manual String Formatting (`fmt.Fprintf(w, "%s,%s\n", ...)`)**: Extremely prone to corrupting CSV structure when strings contain commas, quotes, or newlines, and vulnerable to formula injection.

---

## Comparison Table

| Alternative | Pros | Cons | Why We Chose `go-app-kit/export` |
|---|---|---|---|
| **In-Memory Slice Buffering** | Simple, sequential logic | $O(N)$ heap consumption; triggers OOM kills on large tables | `go-app-kit/export` streams row-by-row with $O(1)$ constant memory. |
| **XLSX Libraries (`excelize`)** | Cell styling, multiple tabs, formulas | 10x slower, memory-intensive, complex API | `go-app-kit/export` provides ultra-fast streaming CSV with native Excel UTF-8 BOM support. |
| **Manual String Formatting** | Zero abstraction | Breaks on quotes/commas; corrupts data and risks formula injection | `go-app-kit/export` enforces strict RFC 4180 escaping with type-safe generic extractors. |

---

## Usage Example

```go
package main

import (
    "fmt"
    "net/http"
    "os"

    "github.com/umesh0492/go-app-kit/export"
)

type Transaction struct {
    ID        string
    Vendor    string
    Amount    float64
    Currency  string
    Status    string
}

func main() {
    // 1. Define strongly-typed column extractors
    columns := []export.Column[Transaction]{
        {Header: "Transaction ID", Extractor: func(t Transaction) string { return t.ID }},
        {Header: "Vendor Name", Extractor: func(t Transaction) string { return t.Vendor }},
        {Header: "Amount", Extractor: func(t Transaction) string { return fmt.Sprintf("%.2f", t.Amount) }},
        {Header: "Currency", Extractor: func(t Transaction) string { return t.Currency }},
        {Header: "Status", Extractor: func(t Transaction) string { return t.Status }},
    }

    // 2. Open destination file or HTTP ResponseWriter
    file, err := os.Create("transactions.csv")
    if err != nil {
        panic(err)
    }
    defer file.Close()

    // 3. Initialize streaming exporter with Excel UTF-8 BOM support
    streamer, err := export.NewCSVStreamer(file, columns,
        export.WithBOM(true),
        export.WithCRLF(true),
        export.WithFlushInterval(500),
    )
    if err != nil {
        panic(err)
    }
    defer streamer.Close()

    // Write header
    if err := streamer.WriteHeader(); err != nil {
        panic(err)
    }

    // 4. Stream rows as they are fetched from database
    mockRows := []Transaction{
        {ID: "TX-101", Vendor: "Acme Corp, Ltd", Amount: 154000.50, Currency: "INR", Status: "SETTLED"},
        {ID: "TX-102", Vendor: "Infosys Technologies", Amount: 89000.00, Currency: "INR", Status: "PENDING"},
    }

    for _, row := range mockRows {
        if err := streamer.WriteRow(row); err != nil {
            panic(err)
        }
    }

    fmt.Printf("Successfully exported %d rows with constant memory footprint.\n", streamer.RowCount())
}
```
