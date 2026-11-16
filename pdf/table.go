package pdf

import (
	"fmt"
	"strings"

	"github.com/signintech/gopdf"
)

// Color 是 RGB 颜色。
type Color struct {
	R uint8
	G uint8
	B uint8
}

// Table 是要绘制的表格。
type Table struct {
	// Headers 为空时不绘制表头。
	Headers []string
	Rows    [][]string
	// Widths 是列宽权重；为空或全为 0 时等宽。
	Widths []float64
	// Align 是按列的对齐方式：left / center / right，默认 left。
	Align []string
	// Zebra 为偶数数据行添加浅色背景。
	Zebra bool
	// HeaderFill 是表头背景色，默认深蓝色。
	HeaderFill Color
	// FontSize 是表格字号，默认正文 - 1。
	FontSize float64
}

// Table 绘制表格，自动换行、自动分页并在新页重复表头。
func (d *Document) Table(table Table) error {
	if d.finalized {
		return ErrClosed
	}
	width := tableColumnCount(table)
	if width == 0 {
		return nil
	}

	fontSize := table.FontSize
	if fontSize <= 0 {
		fontSize = d.opts.FontSize - 1
	}
	if fontSize < 7 {
		fontSize = 7
	}
	if err := d.pdf.SetFont(d.opts.FontFamily, "", fontSize); err != nil {
		return fmt.Errorf("pdf: set table font: %w", err)
	}

	widths := tableColumnWidths(table, width, d.contentWidth())
	lineHeight := fontSize * 1.35
	padding := cellPadding

	headers := table.Headers
	if len(headers) > 0 {
		headers = padRow(headers, width)
	}
	headerHeight := d.tableRowHeight(headers, widths, lineHeight, padding)
	rowHeights := make([]float64, len(table.Rows))
	for i, row := range table.Rows {
		rowHeights[i] = d.tableRowHeight(row, widths, lineHeight, padding)
	}

	if len(headers) > 0 {
		d.ensureSpace(headerHeight)
		d.drawTableHeader(headers, widths, headerHeight, lineHeight, padding, table)
	}

	for i, row := range table.Rows {
		height := rowHeights[i]
		if d.y+height > d.contentBottom() {
			d.addPage()
			if len(headers) > 0 {
				d.drawTableHeader(headers, widths, headerHeight, lineHeight, padding, table)
			}
		}

		row = padRow(row, width)
		if table.Zebra && i%2 == 1 {
			d.pdf.SetFillColor(245, 246, 248)
			d.pdf.RectFromUpperLeftWithStyle(d.opts.Margins.Left, d.y, d.contentWidth(), height, "F")
		}

		d.pdf.SetTextColor(33, 37, 41)
		x := d.opts.Margins.Left
		for col := 0; col < width; col++ {
			lines, err := d.wrap(row[col], widths[col]-padding*2)
			if err != nil {
				return err
			}
			align := tableAlign(table.Align, col)
			for lineIndex, line := range lines {
				if line == "" {
					continue
				}
				lineWidth, err := d.pdf.MeasureTextWidth(line)
				if err != nil {
					return fmt.Errorf("pdf: measure table text: %w", err)
				}
				drawX := x + padding
				switch align {
				case "center":
					drawX = x + (widths[col]-lineWidth)/2
				case "right":
					drawX = x + widths[col] - lineWidth - padding
				}
				d.pdf.SetXY(drawX, d.y+padding+float64(lineIndex)*lineHeight)
				_ = d.pdf.Cell(&gopdf.Rect{W: widths[col], H: lineHeight}, line)
			}
			x += widths[col]
		}
		d.drawTableBorders(widths, height)
		d.y += height
	}
	d.y += 8
	return nil
}

func (d *Document) tableRowHeight(row []string, widths []float64, lineHeight, padding float64) float64 {
	maxLines := 1
	for col := range widths {
		cell := ""
		if col < len(row) {
			cell = row[col]
		}
		lines, err := d.wrap(cell, widths[col]-padding*2)
		if err != nil {
			continue
		}
		if len(lines) > maxLines {
			maxLines = len(lines)
		}
	}
	return float64(maxLines)*lineHeight + padding*2
}

func (d *Document) drawTableHeader(headers []string, widths []float64, height, lineHeight, padding float64, table Table) {
	fill := table.HeaderFill
	if fill == (Color{}) {
		fill = Color{R: 31, G: 78, B: 120}
	}
	d.pdf.SetFillColor(fill.R, fill.G, fill.B)
	d.pdf.RectFromUpperLeftWithStyle(d.opts.Margins.Left, d.y, d.contentWidth(), height, "F")

	d.pdf.SetTextColor(255, 255, 255)
	x := d.opts.Margins.Left
	for col := 0; col < len(headers); col++ {
		lines, _ := d.wrap(headers[col], widths[col]-padding*2)
		for lineIndex, line := range lines {
			if line == "" {
				continue
			}
			lineWidth, _ := d.pdf.MeasureTextWidth(line)
			drawX := x + (widths[col]-lineWidth)/2
			d.pdf.SetXY(drawX, d.y+padding+float64(lineIndex)*lineHeight)
			_ = d.pdf.Cell(&gopdf.Rect{W: widths[col], H: lineHeight}, line)
		}
		x += widths[col]
	}
	d.drawTableBorders(widths, height)
	d.y += height
}

func (d *Document) drawTableBorders(widths []float64, height float64) {
	d.pdf.SetStrokeColor(210, 214, 220)
	d.pdf.SetLineWidth(0.4)
	left := d.opts.Margins.Left
	right := left
	for _, width := range widths {
		right += width
	}
	top := d.y
	bottom := d.y + height
	d.pdf.Line(left, top, right, top)
	d.pdf.Line(left, bottom, right, bottom)
	x := left
	for _, width := range widths {
		d.pdf.Line(x, top, x, bottom)
		x += width
	}
	d.pdf.Line(right, top, right, bottom)
}

func tableColumnCount(table Table) int {
	width := len(table.Headers)
	for _, row := range table.Rows {
		if len(row) > width {
			width = len(row)
		}
	}
	if len(table.Widths) > width {
		width = len(table.Widths)
	}
	return width
}

func tableColumnWidths(table Table, columns int, contentWidth float64) []float64 {
	weights := make([]float64, columns)
	total := 0.0
	for i := range weights {
		if i < len(table.Widths) && table.Widths[i] > 0 {
			weights[i] = table.Widths[i]
		} else {
			weights[i] = 1
		}
		total += weights[i]
	}
	widths := make([]float64, columns)
	for i := range weights {
		widths[i] = contentWidth * weights[i] / total
	}
	return widths
}

func tableAlign(align []string, col int) string {
	if col < len(align) {
		switch strings.ToLower(strings.TrimSpace(align[col])) {
		case "center":
			return "center"
		case "right":
			return "right"
		}
	}
	return "left"
}

func padRow(row []string, width int) []string {
	padded := make([]string, width)
	copy(padded, row)
	return padded
}
