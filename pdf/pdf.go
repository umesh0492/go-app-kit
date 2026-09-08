// Package pdf provides in-memory HTML-to-PDF compilation via wkhtmltopdf
// with production-ready options, template helpers, and document templates.
package pdf

import (
	"bytes"
	"fmt"
	"html/template"
	"strings"

	wkhtml "github.com/SebastiaanKlippert/go-wkhtmltopdf"
)

// Generator defines the interface representing wkhtml.PDFGenerator's methods to allow mock unit tests.
type Generator interface {
	AddPage(*wkhtml.PageReader)
	Create() error
	Bytes() []byte
}

type wkhtmlGenerator struct {
	*wkhtml.PDFGenerator
}

func (w *wkhtmlGenerator) AddPage(p *wkhtml.PageReader) {
	w.PDFGenerator.AddPage(p)
}

func (w *wkhtmlGenerator) Create() error {
	return w.PDFGenerator.Create()
}

func (w *wkhtmlGenerator) Bytes() []byte {
	return w.PDFGenerator.Bytes()
}

// NewWkhtmlGenerator constructs a generator wrapping a raw wkhtml.PDFGenerator.
func NewWkhtmlGenerator(g *wkhtml.PDFGenerator) Generator {
	return &wkhtmlGenerator{PDFGenerator: g}
}

// GeneratorFunc defines the constructor signature for creating a PDF generator.
type GeneratorFunc func(opts Options) (Generator, error)

// Options configures PDF rendering properties.
type Options struct {
	PageSize              string        // "A4", "Letter", etc. (Default: "A4")
	Orientation           string        // "Portrait", "Landscape" (Default: "Portrait")
	DPI                   uint          // DPI resolution (Default: 300)
	MarginTop             uint          // Margins in mm (Default: 10)
	MarginBottom          uint          // Margins in mm (Default: 10)
	MarginLeft            uint          // Margins in mm (Default: 10)
	MarginRight           uint          // Margins in mm (Default: 10)
	Title                 string        // Document title
	EnableLocalFileAccess bool          // Default: false (prevents file:/// exfiltration)
	GeneratorFunc         GeneratorFunc // Custom generator constructor
}

// Option modifies Options.
type Option func(*Options)

// DefaultOptions returns standard A4 portrait settings with 10mm margins.
// Note: EnableLocalFileAccess defaults to false to prevent local file inclusion.
func DefaultOptions() Options {
	return Options{
		PageSize:              "A4",
		Orientation:           "Portrait",
		DPI:                   300,
		MarginTop:             10,
		MarginBottom:          10,
		MarginLeft:            10,
		MarginRight:           10,
		EnableLocalFileAccess: false,
	}
}

// WithPageSize sets the page size (e.g. "A4", "Letter").
func WithPageSize(size string) Option {
	return func(o *Options) { o.PageSize = size }
}

// WithOrientation sets page orientation ("Portrait" or "Landscape").
func WithOrientation(orientation string) Option {
	return func(o *Options) { o.Orientation = orientation }
}

// WithMargins sets top, bottom, left, and right margins in millimeters.
func WithMargins(top, bottom, left, right uint) Option {
	return func(o *Options) {
		o.MarginTop = top
		o.MarginBottom = bottom
		o.MarginLeft = left
		o.MarginRight = right
	}
}

// WithDPI sets rendering DPI.
func WithDPI(dpi uint) Option {
	return func(o *Options) { o.DPI = dpi }
}

// WithTitle sets document title metadata.
func WithTitle(title string) Option {
	return func(o *Options) { o.Title = title }
}

// WithLocalFileAccess controls whether local file access is permitted during rendering.
// By default, this is disabled (false) to prevent SSRF and arbitrary local file exfiltration (e.g. file:///etc/passwd).
func WithLocalFileAccess(enable bool) Option {
	return func(o *Options) { o.EnableLocalFileAccess = enable }
}

// WithGeneratorFunc configures a custom generator constructor.
func WithGeneratorFunc(fn GeneratorFunc) Option {
	return func(o *Options) { o.GeneratorFunc = fn }
}

// WithGenerator configures a specific Generator instance.
func WithGenerator(g Generator) Option {
	return func(o *Options) {
		o.GeneratorFunc = func(Options) (Generator, error) {
			return g, nil
		}
	}
}

// defaultGenerator is the unexported default constructor for wkhtml.PDFGenerator.
func defaultGenerator(opts Options) (Generator, error) {
	g, err := wkhtml.NewPDFGenerator()
	if err != nil {
		return nil, fmt.Errorf("failed to initialize wkhtmltopdf: %w", err)
	}
	g.Dpi.Set(opts.DPI)
	g.PageSize.Set(opts.PageSize)
	g.Orientation.Set(opts.Orientation)
	g.MarginTop.Set(opts.MarginTop)
	g.MarginBottom.Set(opts.MarginBottom)
	g.MarginLeft.Set(opts.MarginLeft)
	g.MarginRight.Set(opts.MarginRight)
	if opts.Title != "" {
		g.Title.Set(opts.Title)
	}
	return &wkhtmlGenerator{PDFGenerator: g}, nil
}

// NewGenerator is a constructor function for creating a Generator instance with the given options.
func NewGenerator(opts Options) (Generator, error) {
	return defaultGenerator(opts)
}

// Generate accepts a raw HTML string and optional configurations, compiling it into a PDF bytes buffer.
func Generate(html string, opts ...Option) (*bytes.Buffer, error) {
	config := DefaultOptions()
	for _, opt := range opts {
		opt(&config)
	}

	genFunc := config.GeneratorFunc
	if genFunc == nil {
		genFunc = defaultGenerator
	}

	pdfg, err := genFunc(config)
	if err != nil {
		return nil, err
	}

	page := wkhtml.NewPageReader(bytes.NewBufferString(html))
	page.EnableLocalFileAccess.Set(config.EnableLocalFileAccess)
	page.Encoding.Set("UTF-8")

	pdfg.AddPage(page)

	if err := pdfg.Create(); err != nil {
		return nil, fmt.Errorf("failed to render pdf: %w", err)
	}

	return bytes.NewBuffer(pdfg.Bytes()), nil
}

// RenderTemplate evaluates an HTML Go template string against a data context.
func RenderTemplate(tmplStr string, data any) (string, error) {
	tmpl, err := template.New("pdf").Funcs(template.FuncMap{
		"upper": strings.ToUpper,
		"lower": strings.ToLower,
	}).Parse(tmplStr)
	if err != nil {
		return "", fmt.Errorf("failed to parse template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to execute template: %w", err)
	}

	return buf.String(), nil
}

// GenerateFromTemplate executes an HTML template with the given data context and renders it into a PDF buffer.
func GenerateFromTemplate(tmplStr string, data any, opts ...Option) (*bytes.Buffer, error) {
	rendered, err := RenderTemplate(tmplStr, data)
	if err != nil {
		return nil, err
	}
	return Generate(rendered, opts...)
}
