package excel

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"unicode"

	"github.com/xuri/excelize/v2"
)

// ExportXLSX 将表格写入 XLSX。
func ExportXLSX(w io.Writer, tables []Table, opts ...Option) error {
	options := applyExportOptions(opts)
	file := excelize.NewFile()
	defer func() { _ = file.Close() }()

	used := make(map[string]bool)
	for i, table := range tables {
		sheet := uniqueSheetName(table.Sheet, i, used)
		if i == 0 {
			if err := file.SetSheetName("Sheet1", sheet); err != nil {
				return fmt.Errorf("excel: rename default sheet: %w", err)
			}
		} else if _, err := file.NewSheet(sheet); err != nil {
			return fmt.Errorf("excel: create sheet %q: %w", sheet, err)
		}
		if err := writeXLSXSheet(file, sheet, table, options); err != nil {
			return err
		}
	}

	buffer, err := file.WriteToBuffer()
	if err != nil {
		return fmt.Errorf("excel: write xlsx: %w", err)
	}
	if _, err := w.Write(buffer.Bytes()); err != nil {
		return fmt.Errorf("excel: write output: %w", err)
	}
	return nil
}

func writeXLSXSheet(file *excelize.File, sheet string, table Table, options ExportOptions) error {
	width := tableWidth(table, options.ColumnWidths)
	if width == 0 {
		return nil
	}

	row := 1
	if len(table.Headers) > 0 {
		for col := 0; col < width; col++ {
			value := ""
			if col < len(table.Headers) {
				value = table.Headers[col]
			}
			cell, _ := excelize.CoordinatesToCellName(col+1, row)
			if err := file.SetCellValue(sheet, cell, value); err != nil {
				return fmt.Errorf("excel: set header: %w", err)
			}
		}
		row++
	}

	for _, dataRow := range table.Rows {
		for col := 0; col < width; col++ {
			value := ""
			if col < len(dataRow) {
				value = dataRow[col]
			}
			cell, _ := excelize.CoordinatesToCellName(col+1, row)
			if err := file.SetCellValue(sheet, cell, value); err != nil {
				return fmt.Errorf("excel: set cell: %w", err)
			}
		}
		row++
	}

	// 表头样式。
	if len(table.Headers) > 0 {
		style, err := headerStyle(file, options)
		if err != nil {
			return err
		}
		lastCol, _ := excelize.ColumnNumberToName(width)
		if err := file.SetCellStyle(sheet, "A1", fmt.Sprintf("%s1", lastCol), style); err != nil {
			return fmt.Errorf("excel: set header style: %w", err)
		}
		if options.FreezeHeader {
			_ = file.SetPanes(sheet, &excelize.Panes{
				Freeze:      true,
				Split:       false,
				YSplit:      1,
				TopLeftCell: "A2",
				ActivePane:  "bottomLeft",
			})
		}
		if options.AutoFilter {
			lastRow := row - 1
			if lastRow < 1 {
				lastRow = 1
			}
			_ = file.AutoFilter(sheet, fmt.Sprintf("A1:%s%d", lastCol, lastRow), nil)
		}
	}

	// 斑马纹。
	if options.Zebra && len(table.Rows) > 0 {
		style, err := zebraStyle(file)
		if err != nil {
			return err
		}
		start := 2
		if len(table.Headers) == 0 {
			start = 1
		}
		lastCol, _ := excelize.ColumnNumberToName(width)
		for i := range table.Rows {
			if i%2 == 1 {
				rowNo := start + i
				_ = file.SetCellStyle(sheet, fmt.Sprintf("A%d", rowNo), fmt.Sprintf("%s%d", lastCol, rowNo), style)
			}
		}
	}

	// 列宽：优先使用配置，否则按内容估算。
	for col := 1; col <= width; col++ {
		widthValue := 12.0
		if col-1 < len(options.ColumnWidths) && options.ColumnWidths[col-1] > 0 {
			widthValue = options.ColumnWidths[col-1]
		} else {
			widthValue = estimateColumnWidth(col-1, table)
		}
		name, _ := excelize.ColumnNumberToName(col)
		_ = file.SetColWidth(sheet, name, name, widthValue)
	}
	return nil
}

func headerStyle(file *excelize.File, options ExportOptions) (int, error) {
	fill := options.HeaderFill
	if fill == "" {
		fill = "1F4E78"
	}
	fontColor := options.HeaderFontColor
	if fontColor == "" {
		fontColor = "FFFFFF"
	}
	return file.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true, Color: fontColor},
		Fill:      excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{fill}},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center", WrapText: true},
		Border:    thinBorders("D9D9D9"),
	})
}

func zebraStyle(file *excelize.File) (int, error) {
	return file.NewStyle(&excelize.Style{
		Fill:   excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"F2F2F2"}},
		Border: thinBorders("D9D9D9"),
	})
}

func thinBorders(color string) []excelize.Border {
	return []excelize.Border{
		{Type: "left", Color: color, Style: 1},
		{Type: "right", Color: color, Style: 1},
		{Type: "top", Color: color, Style: 1},
		{Type: "bottom", Color: color, Style: 1},
	}
}

func tableWidth(table Table, widths []float64) int {
	width := len(table.Headers)
	for _, row := range table.Rows {
		if len(row) > width {
			width = len(row)
		}
	}
	if len(widths) > width {
		width = len(widths)
	}
	return width
}

func estimateColumnWidth(col int, table Table) float64 {
	maxWidth := 0
	if col < len(table.Headers) {
		maxWidth = displayWidth(table.Headers[col])
	}
	limit := len(table.Rows)
	if limit > 200 {
		limit = 200
	}
	for i := 0; i < limit; i++ {
		if col < len(table.Rows[i]) {
			if w := displayWidth(table.Rows[i][col]); w > maxWidth {
				maxWidth = w
			}
		}
	}
	width := float64(maxWidth) + 2
	if width < 8 {
		width = 8
	}
	if width > 60 {
		width = 60
	}
	return width
}

func displayWidth(value string) int {
	width := 0
	for _, r := range value {
		if unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hangul, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) {
			width += 2
		} else {
			width++
		}
	}
	return width
}

func uniqueSheetName(name string, index int, used map[string]bool) string {
	name = sanitizeSheetName(name)
	if name == "" {
		name = fmt.Sprintf("Sheet%d", index+1)
	}
	base := name
	for i := 2; used[strings.ToLower(name)]; i++ {
		name = truncateSheetName(fmt.Sprintf("%s_%d", base, i))
	}
	used[strings.ToLower(name)] = true
	return name
}

func sanitizeSheetName(name string) string {
	name = strings.TrimSpace(name)
	replacer := strings.NewReplacer("/", "_", "\\", "_", "?", "_", "*", "_", "[", "_", "]", "_", ":", "_")
	name = replacer.Replace(name)
	name = truncateSheetName(name)
	if name == "" || name == "History" {
		return ""
	}
	return name
}

func truncateSheetName(name string) string {
	runes := []rune(name)
	if len(runes) > 31 {
		name = string(runes[:31])
	}
	return strings.TrimSpace(name)
}

// readXLSX 解析 XLSX。
func readXLSX(data []byte, options ImportOptions) ([]Table, error) {
	file, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("excel: open xlsx: %w", err)
	}
	defer func() { _ = file.Close() }()

	sheets := file.GetSheetList()
	if options.Sheet != "" {
		found := false
		for _, sheet := range sheets {
			if sheet == options.Sheet {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("excel: sheet %q not found", options.Sheet)
		}
		sheets = []string{options.Sheet}
	}

	tables := make([]Table, 0, len(sheets))
	for _, sheet := range sheets {
		table, err := readXLSXSheet(file, sheet, options)
		if err != nil {
			return nil, err
		}
		tables = append(tables, table)
	}
	return tables, nil
}

func readXLSXSheet(file *excelize.File, sheet string, options ImportOptions) (Table, error) {
	rows, err := file.Rows(sheet)
	if err != nil {
		return Table{}, fmt.Errorf("excel: open sheet %q: %w", sheet, err)
	}
	defer func() { _ = rows.Close() }()

	var headers []string
	var data [][]string
	rowNo := 0
	for rows.Next() {
		rowNo++
		columns, err := rows.Columns()
		if err != nil {
			return Table{}, fmt.Errorf("excel: read sheet %q: %w", sheet, err)
		}
		columns = normalizeCells(columns, options)
		if rowNo < options.HeaderRow {
			continue
		}
		if rowNo == options.HeaderRow {
			headers = trimTrailingEmpty(columns)
			if options.MaxCols > 0 && len(headers) > options.MaxCols {
				headers = headers[:options.MaxCols]
			}
			continue
		}
		if isEmptyRow(columns) {
			continue
		}
		if options.MaxCols > 0 && len(columns) > options.MaxCols {
			columns = columns[:options.MaxCols]
		}
		data = append(data, columns)
		if options.MaxRows > 0 && len(data) >= options.MaxRows {
			break
		}
	}
	if err := rows.Error(); err != nil {
		return Table{}, fmt.Errorf("excel: iterate sheet %q: %w", sheet, err)
	}
	return finalizeTable(sheet, headers, data, options), nil
}

func normalizeCells(cells []string, options ImportOptions) []string {
	result := make([]string, len(cells))
	for i, cell := range cells {
		if options.Trim {
			cell = strings.TrimSpace(cell)
		}
		result[i] = cell
	}
	return result
}

func trimTrailingEmpty(cells []string) []string {
	last := -1
	for i, cell := range cells {
		if strings.TrimSpace(cell) != "" {
			last = i
		}
	}
	if last < 0 {
		return nil
	}
	return append([]string(nil), cells[:last+1]...)
}

func isEmptyRow(cells []string) bool {
	for _, cell := range cells {
		if strings.TrimSpace(cell) != "" {
			return false
		}
	}
	return true
}

// finalizeTable 统一补齐列、生成缺省表头并应用最大列数限制。
func finalizeTable(sheet string, headers []string, rows [][]string, options ImportOptions) Table {
	width := len(headers)
	for _, row := range rows {
		if len(row) > width {
			width = len(row)
		}
	}
	if options.MaxCols > 0 && width > options.MaxCols {
		width = options.MaxCols
	}

	result := Table{Sheet: sheet, Rows: make([][]string, 0, len(rows))}
	result.Headers = make([]string, width)
	copy(result.Headers, headers)
	for i := len(headers); i < width; i++ {
		result.Headers[i] = fmt.Sprintf("Column%d", i+1)
	}
	for _, row := range rows {
		normalized := make([]string, width)
		copy(normalized, row)
		result.Rows = append(result.Rows, normalized)
	}
	return result
}
