// Package excel 提供 XLSX / XLS 文件的导入与导出能力。
//
// 支持范围：
//   - XLSX：读写，基于 excelize；
//   - XLS：读取 BIFF8（基于 extrame/xls），导出为 Excel 2003 SpreadsheetML
//     兼容格式（.xls 扩展名，Excel/WPS 可直接打开）；
//   - 自动按文件魔数识别格式，不依赖扩展名；
//   - 支持多工作表、表头冻结、自动筛选、列宽、斑马纹；
//   - 导入支持表头行、最大行数/列数、单元格去空格。
//
// 说明：SpreadsheetML 是 XML 格式而非 BIFF 二进制；如果需要严格 BIFF8 导出，
// 建议使用专门的商业组件或 Office 自动化。
package excel

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

// ErrUnsupportedFormat 表示不支持的文件格式。
var ErrUnsupportedFormat = errors.New("excel: unsupported format")

// Table 是一张工作表的数据。
// Headers 可以为空；Rows 中每行长度不要求一致，导出时会自动补齐列。
type Table struct {
	Sheet   string     `json:"sheet"`
	Headers []string   `json:"headers,omitempty"`
	Rows    [][]string `json:"rows"`
}

// ExportOptions 是导出配置。
type ExportOptions struct {
	// FreezeHeader 冻结表头行。
	FreezeHeader bool
	// AutoFilter 为表头添加自动筛选。
	AutoFilter bool
	// Zebra 为偶数行添加浅色背景。
	Zebra bool
	// ColumnWidths 指定列宽；为空时按内容自动估算。
	ColumnWidths []float64
	// HeaderFill / HeaderFontColor 是表头背景色与字体颜色（十六进制 RRGGBB）。
	HeaderFill      string
	HeaderFontColor string
}

// Option 是导出选项函数。
type Option func(*ExportOptions)

// WithFreezeHeader 设置是否冻结表头。
func WithFreezeHeader(enabled bool) Option {
	return func(o *ExportOptions) { o.FreezeHeader = enabled }
}

// WithAutoFilter 设置是否添加自动筛选。
func WithAutoFilter(enabled bool) Option {
	return func(o *ExportOptions) { o.AutoFilter = enabled }
}

// WithZebra 设置是否使用斑马纹。
func WithZebra(enabled bool) Option {
	return func(o *ExportOptions) { o.Zebra = enabled }
}

// WithColumnWidths 指定列宽。
func WithColumnWidths(widths ...float64) Option {
	return func(o *ExportOptions) { o.ColumnWidths = append([]float64(nil), widths...) }
}

// WithHeaderStyle 设置表头背景色与字体颜色（RRGGBB）。
func WithHeaderStyle(fill, fontColor string) Option {
	return func(o *ExportOptions) {
		if fill != "" {
			o.HeaderFill = fill
		}
		if fontColor != "" {
			o.HeaderFontColor = fontColor
		}
	}
}

func defaultExportOptions() ExportOptions {
	return ExportOptions{
		FreezeHeader:    true,
		AutoFilter:      true,
		HeaderFill:      "1F4E78",
		HeaderFontColor: "FFFFFF",
	}
}

// ImportOptions 是导入配置。
type ImportOptions struct {
	// Sheet 指定工作表；为空时导入全部工作表。
	Sheet string
	// HeaderRow 是表头所在行（1 开始），默认 1。
	HeaderRow int
	// MaxRows 是每张表最多导入的数据行数（不含表头），0 表示不限制。
	MaxRows int
	// MaxCols 是每张表最多导入的列数，0 表示不限制。
	MaxCols int
	// MaxBytes 是输入文件大小上限，默认 64MB。
	MaxBytes int64
	// Trim 是否去除单元格首尾空格，默认 true。
	Trim bool
}

// ImportOption 是导入选项函数。
type ImportOption func(*ImportOptions)

// WithSheet 指定导入的工作表。
func WithSheet(name string) ImportOption {
	return func(o *ImportOptions) { o.Sheet = name }
}

// WithHeaderRow 指定表头行号（1 开始）。
func WithHeaderRow(row int) ImportOption {
	return func(o *ImportOptions) { o.HeaderRow = row }
}

// WithMaxRows 限制每张表导入的数据行数。
func WithMaxRows(rows int) ImportOption {
	return func(o *ImportOptions) { o.MaxRows = rows }
}

// WithMaxCols 限制每张表导入的列数。
func WithMaxCols(cols int) ImportOption {
	return func(o *ImportOptions) { o.MaxCols = cols }
}

// WithMaxBytes 限制输入文件大小。
func WithMaxBytes(size int64) ImportOption {
	return func(o *ImportOptions) {
		if size > 0 {
			o.MaxBytes = size
		}
	}
}

// WithTrim 设置是否去除单元格首尾空格。
func WithTrim(enabled bool) ImportOption {
	return func(o *ImportOptions) { o.Trim = enabled }
}

func defaultImportOptions() ImportOptions {
	return ImportOptions{HeaderRow: 1, MaxBytes: 64 << 20, Trim: true}
}

func applyExportOptions(opts []Option) ExportOptions {
	options := defaultExportOptions()
	for _, opt := range opts {
		opt(&options)
	}
	return options
}

func applyImportOptions(opts []ImportOption) ImportOptions {
	options := defaultImportOptions()
	for _, opt := range opts {
		opt(&options)
	}
	if options.HeaderRow <= 0 {
		options.HeaderRow = 1
	}
	if options.MaxBytes <= 0 {
		options.MaxBytes = 64 << 20
	}
	return options
}

// Export 按文件名扩展名导出表格。
// 空文件名默认 XLSX；.xlsx 使用 excelize，.xls 使用 SpreadsheetML 兼容格式。
func Export(w io.Writer, filename string, tables []Table, opts ...Option) error {
	if w == nil {
		return fmt.Errorf("excel: writer is nil")
	}
	if len(tables) == 0 {
		return fmt.Errorf("excel: no tables to export")
	}
	switch strings.ToLower(filepath.Ext(filename)) {
	case "", ".xlsx", ".xlsm":
		return ExportXLSX(w, tables, opts...)
	case ".xls":
		return ExportXLS(w, tables, opts...)
	default:
		return fmt.Errorf("%w: %s", ErrUnsupportedFormat, filename)
	}
}

// Read 自动识别并读取 XLSX / XLS / SpreadsheetML。
func Read(r io.Reader, filename string, opts ...ImportOption) ([]Table, error) {
	if r == nil {
		return nil, fmt.Errorf("excel: reader is nil")
	}
	options := applyImportOptions(opts)
	data, err := io.ReadAll(io.LimitReader(r, options.MaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("excel: read input: %w", err)
	}
	if int64(len(data)) > options.MaxBytes {
		return nil, fmt.Errorf("excel: file exceeds %d bytes", options.MaxBytes)
	}
	return readData(data, filename, options)
}

// readData 按魔数选择解析器，扩展名仅作为兜底提示。
func readData(data []byte, filename string, options ImportOptions) ([]Table, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("excel: empty file")
	}
	switch {
	case bytes.HasPrefix(data, []byte("PK\x03\x04")), bytes.HasPrefix(data, []byte("PK\x05\x06")):
		return readXLSX(data, options)
	case bytes.HasPrefix(data, []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}):
		return readBIFF(data, options)
	case bytes.HasPrefix(bytes.TrimSpace(data), []byte("<?xml")), bytes.Contains(data[:min(len(data), 512)], []byte("<Workbook")):
		return readSpreadsheetML(data, options)
	}

	switch strings.ToLower(filepath.Ext(filename)) {
	case ".xlsx", ".xlsm":
		return readXLSX(data, options)
	case ".xls":
		return nil, fmt.Errorf("excel: cannot recognize xls format")
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedFormat, filename)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
