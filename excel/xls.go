package excel

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"

	"github.com/extrame/xls"
)

// ExportXLS 将表格导出为 Excel 2003 SpreadsheetML（.xls 扩展名）。
// 该格式是 XML，Excel 与 WPS 可直接打开；它不是 BIFF8 二进制。
func ExportXLS(w io.Writer, tables []Table, opts ...Option) error {
	options := applyExportOptions(opts)
	var buf bytes.Buffer
	buf.WriteString(xml.Header)
	buf.WriteString(`<Workbook xmlns="urn:schemas-microsoft-com:office:spreadsheet" xmlns:ss="urn:schemas-microsoft-com:office:spreadsheet">`)
	buf.WriteString(`<Styles><Style ss:ID="header"><Font ss:Bold="1" ss:Color="#`)
	buf.WriteString(escapeXML(options.HeaderFontColor))
	buf.WriteString(`"/><Interior ss:Color="#`)
	buf.WriteString(escapeXML(options.HeaderFill))
	buf.WriteString(`" ss:Pattern="Solid"/></Style></Styles>`)

	used := make(map[string]bool)
	for i, table := range tables {
		sheet := uniqueSheetName(table.Sheet, i, used)
		fmt.Fprintf(&buf, `<Worksheet ss:Name="%s"><Table>`, escapeXML(sheet))
		width := tableWidth(table, options.ColumnWidths)

		for col := 0; col < width; col++ {
			widthValue := 80
			if col < len(options.ColumnWidths) && options.ColumnWidths[col] > 0 {
				widthValue = int(options.ColumnWidths[col] * 7)
			}
			fmt.Fprintf(&buf, `<Column ss:Index="%d" ss:Width="%d"/>`, col+1, widthValue)
		}

		if len(table.Headers) > 0 {
			writeXMLRow(&buf, table.Headers, width, "header")
		}
		for _, row := range table.Rows {
			writeXMLRow(&buf, row, width, "")
		}
		buf.WriteString(`</Table></Worksheet>`)
	}
	buf.WriteString(`</Workbook>`)

	if _, err := w.Write(buf.Bytes()); err != nil {
		return fmt.Errorf("excel: write xls: %w", err)
	}
	return nil
}

func writeXMLRow(w io.Writer, row []string, width int, style string) {
	if style != "" {
		fmt.Fprintf(w, `<Row ss:StyleID="%s">`, escapeXML(style))
	} else {
		w.Write([]byte(`<Row>`))
	}
	for col := 0; col < width; col++ {
		value := ""
		if col < len(row) {
			value = row[col]
		}
		fmt.Fprintf(w, `<Cell><Data ss:Type="String">%s</Data></Cell>`, escapeXML(value))
	}
	w.Write([]byte(`</Row>`))
}

func escapeXML(value string) string {
	var buf bytes.Buffer
	_ = xml.EscapeText(&buf, []byte(value))
	return buf.String()
}

// readSpreadsheetML 解析 SpreadsheetML。
func readSpreadsheetML(data []byte, options ImportOptions) ([]Table, error) {
	var workbook xmlWorkbook
	if err := xml.Unmarshal(data, &workbook); err != nil {
		return nil, fmt.Errorf("excel: parse spreadsheetml: %w", err)
	}
	tables := make([]Table, 0, len(workbook.Worksheets))
	for _, sheet := range workbook.Worksheets {
		name := sheet.Name
		if name == "" {
			name = fmt.Sprintf("Sheet%d", len(tables)+1)
		}
		if options.Sheet != "" && options.Sheet != name {
			continue
		}
		rows := make([][]string, 0, len(sheet.Table.Rows))
		for _, row := range sheet.Table.Rows {
			cells := make([]string, 0, len(row.Cells))
			for _, cell := range row.Cells {
				if cell.Index > 0 {
					for len(cells) < cell.Index-1 {
						cells = append(cells, "")
					}
				}
				cells = append(cells, cell.Data.Value)
			}
			rows = append(rows, cells)
		}
		tables = append(tables, tableFromRows(name, rows, options))
	}
	if options.Sheet != "" && len(tables) == 0 {
		return nil, fmt.Errorf("excel: sheet %q not found", options.Sheet)
	}
	return tables, nil
}

// readBIFF 解析传统 .xls（BIFF8）文件。
func readBIFF(data []byte, options ImportOptions) ([]Table, error) {
	book, err := xls.OpenReader(bytes.NewReader(data), "utf-8")
	if err != nil {
		return nil, fmt.Errorf("excel: open xls: %w", err)
	}

	tables := make([]Table, 0, book.NumSheets())
	for i := 0; i < book.NumSheets(); i++ {
		sheet := book.GetSheet(i)
		if sheet == nil {
			continue
		}
		name := sheet.Name
		if name == "" {
			name = fmt.Sprintf("Sheet%d", i+1)
		}
		if options.Sheet != "" && options.Sheet != name {
			continue
		}

		rows := make([][]string, 0, int(sheet.MaxRow)+1)
		for rowIndex := 0; rowIndex <= int(sheet.MaxRow); rowIndex++ {
			rows = append(rows, extractXLRow(sheet, rowIndex))
		}
		tables = append(tables, tableFromRows(name, rows, options))
	}
	if options.Sheet != "" && len(tables) == 0 {
		return nil, fmt.Errorf("excel: sheet %q not found", options.Sheet)
	}
	return tables, nil
}

// extractXLRow 读取一行并隔离第三方解析库对损坏文件可能产生的 panic。
func extractXLRow(sheet *xls.WorkSheet, index int) (cells []string) {
	defer func() {
		if recover() != nil {
			cells = nil
		}
	}()
	row := sheet.Row(index)
	if row == nil {
		return nil
	}
	last := row.LastCol()
	if last < 0 {
		return nil
	}
	cells = make([]string, last+1)
	for col := 0; col <= last; col++ {
		cells[col] = row.Col(col)
	}
	return cells
}

// tableFromRows 将原始行转换为带表头与列数限制的 Table。
func tableFromRows(sheet string, rows [][]string, options ImportOptions) Table {
	var headers []string
	var data [][]string
	for i, row := range rows {
		rowNo := i + 1
		row = normalizeCells(row, options)
		if rowNo < options.HeaderRow {
			continue
		}
		if rowNo == options.HeaderRow {
			headers = trimTrailingEmpty(row)
			if options.MaxCols > 0 && len(headers) > options.MaxCols {
				headers = headers[:options.MaxCols]
			}
			continue
		}
		if isEmptyRow(row) {
			continue
		}
		if options.MaxCols > 0 && len(row) > options.MaxCols {
			row = row[:options.MaxCols]
		}
		data = append(data, row)
		if options.MaxRows > 0 && len(data) >= options.MaxRows {
			break
		}
	}
	return finalizeTable(sheet, headers, data, options)
}

// XML 映射结构。

type xmlWorkbook struct {
	Worksheets []xmlWorksheet `xml:"Worksheet"`
}

type xmlWorksheet struct {
	Name  string   `xml:"Name,attr"`
	Table xmlTable `xml:"Table"`
}

type xmlTable struct {
	Rows []xmlRow `xml:"Row"`
}

type xmlRow struct {
	Cells []xmlCell `xml:"Cell"`
}

type xmlCell struct {
	Index int     `xml:"Index,attr"`
	Data  xmlData `xml:"Data"`
}

type xmlData struct {
	Type  string `xml:"Type,attr"`
	Value string `xml:",chardata"`
}
