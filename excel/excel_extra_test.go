package excel

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/extrame/xls"
	"github.com/xuri/excelize/v2"
)

type failReader struct{ err error }

func (r failReader) Read([]byte) (int, error) { return 0, r.err }

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func readSampleXLS(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/Table.xls")
	if err != nil {
		t.Fatalf("read xls fixture: %v", err)
	}
	return data
}

func TestBIFFRead(t *testing.T) {
	data := readSampleXLS(t)
	tables, err := Read(bytes.NewReader(data), "Table.xls")
	if err != nil {
		t.Fatalf("Read xls: %v", err)
	}
	if len(tables) == 0 {
		t.Fatal("expected at least one sheet")
	}
	name := tables[0].Sheet
	if name == "" {
		t.Fatal("expected sheet name")
	}

	selected, err := Read(bytes.NewReader(data), "Table.xls", WithSheet(name))
	if err != nil {
		t.Fatalf("Read with sheet: %v", err)
	}
	if len(selected) != 1 || selected[0].Sheet != name {
		t.Fatalf("unexpected selection: %+v", selected)
	}
	if _, err := Read(bytes.NewReader(data), "Table.xls", WithSheet("__missing__")); err == nil {
		t.Fatal("expected missing sheet error")
	}

	limited, err := Read(bytes.NewReader(data), "Table.xls",
		WithHeaderRow(2), WithMaxRows(1), WithMaxCols(1), WithTrim(false))
	if err != nil {
		t.Fatalf("Read limited: %v", err)
	}
	if len(limited) == 0 {
		t.Fatal("expected sheet")
	}
	if len(limited[0].Headers) != 1 { //nolint:staticcheck
		t.Fatalf("headers = %v", limited[0].Headers)
	}
	if len(limited[0].Rows) > 1 {
		t.Fatalf("rows = %v", limited[0].Rows)
	}
}

func TestBIFFCorrupt(t *testing.T) {
	data := append([]byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}, []byte("broken")...)
	if _, err := Read(bytes.NewReader(data), "broken.xls"); err == nil {
		t.Fatal("expected corrupt xls error")
	}
}

const spreadsheetML = `<?xml version="1.0"?>
<Workbook xmlns="urn:schemas-microsoft-com:office:spreadsheet" xmlns:ss="urn:schemas-microsoft-com:office:spreadsheet">
<Worksheet ss:Name="S1"><Table>
<Row><Cell><Data ss:Type="String">H1</Data></Cell><Cell><Data ss:Type="String">H2</Data></Cell></Row>
<Row><Cell ss:Index="2"><Data ss:Type="String">B</Data></Cell></Row>
<Row></Row>
</Table></Worksheet>
<Worksheet><Table>
<Row><Cell><Data ss:Type="String">A</Data></Cell></Row>
<Row><Cell><Data ss:Type="String">1</Data></Cell></Row>
<Row><Cell><Data ss:Type="String">2</Data></Cell></Row>
</Table></Worksheet>
</Workbook>`

func TestSpreadsheetMLEdgeCases(t *testing.T) {
	data := []byte(spreadsheetML)
	tables, err := Read(bytes.NewReader(data), "data.xls")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(tables) != 2 {
		t.Fatalf("tables = %d, want 2", len(tables))
	}
	if tables[0].Sheet != "S1" || tables[1].Sheet != "Sheet2" {
		t.Fatalf("sheet names = %q, %q", tables[0].Sheet, tables[1].Sheet)
	}
	// Index 间隔补空单元格。
	if len(tables[0].Rows) != 1 || len(tables[0].Rows[0]) < 2 || tables[0].Rows[0][0] != "" || tables[0].Rows[0][1] != "B" {
		t.Fatalf("rows = %v", tables[0].Rows)
	}

	if got, err := Read(bytes.NewReader(data), "data.xls", WithSheet("S1")); err != nil || len(got) != 1 {
		t.Fatalf("select S1: %v %+v", err, got)
	}
	if _, err := Read(bytes.NewReader(data), "data.xls", WithSheet("nope")); err == nil {
		t.Fatal("expected missing sheet error")
	}

	limited, err := Read(bytes.NewReader([]byte(limitML)), "limit.xls",
		WithHeaderRow(2), WithMaxRows(1), WithMaxCols(1))
	if err != nil {
		t.Fatalf("Read limited: %v", err)
	}
	if len(limited[0].Headers) != 1 || limited[0].Headers[0] != "H1" {
		t.Fatalf("limited headers = %v", limited[0].Headers)
	}
	if len(limited[0].Rows) != 1 || limited[0].Rows[0][0] != "v1" {
		t.Fatalf("limited rows = %v", limited[0].Rows)
	}
}

const limitML = `<?xml version="1.0"?><Workbook><Worksheet ss:Name="L"><Table>` +
	`<Row><Cell><Data>skip</Data></Cell></Row>` +
	`<Row><Cell><Data>H1</Data></Cell><Cell><Data>H2</Data></Cell></Row>` +
	`<Row><Cell><Data>v1</Data></Cell><Cell><Data>v2</Data></Cell></Row>` +
	`<Row><Cell><Data>v3</Data></Cell><Cell><Data>v4</Data></Cell></Row>` +
	`</Table></Worksheet></Workbook>`

func TestSpreadsheetMLInvalid(t *testing.T) {
	bad := []byte("<?xml version=\"1.0\"?><Workbook><Worksheet>")
	if _, err := Read(bytes.NewReader(bad), "bad.xls"); err == nil {
		t.Fatal("expected xml parse error")
	}
}

func TestSpreadsheetMLAllEmptyHeader(t *testing.T) {
	data := []byte(`<?xml version="1.0"?><Workbook><Worksheet ss:Name="E"><Table>` +
		`<Row><Cell><Data ss:Type="String">   </Data></Cell></Row>` +
		`<Row><Cell><Data ss:Type="String">v</Data></Cell></Row>` +
		`</Table></Worksheet></Workbook>`)
	tables, err := Read(bytes.NewReader(data), "e.xls")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(tables[0].Headers) != 1 || tables[0].Headers[0] != "Column1" {
		t.Fatalf("headers = %v", tables[0].Headers)
	}
}

func TestExportOptionsBodies(t *testing.T) {
	var buf bytes.Buffer
	table := Table{Sheet: "opts", Headers: []string{"A"}, Rows: [][]string{{"1"}, {"2"}}}
	if err := Export(&buf, "opts.xlsx", []Table{table},
		WithFreezeHeader(false),
		WithAutoFilter(false),
		WithHeaderStyle("FFAA00", "000000"),
		WithZebra(true),
	); err != nil {
		t.Fatalf("Export: %v", err)
	}
	if err := Export(&buf, "", []Table{table}, WithHeaderStyle("", "")); err != nil {
		t.Fatalf("Export empty filename: %v", err)
	}
	if err := Export(&buf, "opts.xlsm", []Table{table}); err != nil {
		t.Fatalf("Export xlsm: %v", err)
	}
	if err := Export(nil, "opts.xlsx", []Table{table}); err == nil {
		t.Fatal("expected nil writer error")
	}
}

func TestExportXLSColumnWidthsAndWriteError(t *testing.T) {
	var buf bytes.Buffer
	if err := ExportXLS(&buf, []Table{{
		Sheet:   "S",
		Headers: []string{"A", "B"},
		Rows:    [][]string{{"1", "2"}},
	}}, WithColumnWidths(30, 0)); err != nil {
		t.Fatalf("ExportXLS: %v", err)
	}
	if !strings.Contains(buf.String(), `ss:Width="210"`) {
		t.Fatalf("expected custom column width, got %s", buf.String())
	}
	if err := ExportXLS(failWriter{}, []Table{{Headers: []string{"A"}}}); err == nil {
		t.Fatal("expected write error")
	}
	if err := ExportXLS(&buf, nil); err != nil {
		t.Fatalf("ExportXLS empty: %v", err)
	}
}

func TestExportXLSXEdgeCases(t *testing.T) {
	var buf bytes.Buffer
	// 空表宽度为 0。
	if err := ExportXLSX(&buf, []Table{{}}); err != nil {
		t.Fatalf("ExportXLSX empty table: %v", err)
	}
	// 写入失败。
	if err := ExportXLSX(failWriter{}, sampleTables()); err == nil {
		t.Fatal("expected xlsx write error")
	}
	if err := ExportXLSX(&buf, sampleTables(), WithHeaderStyle("FFAA00", "000000")); err != nil {
		t.Fatalf("ExportXLSX styled: %v", err)
	}
}

func TestSheetNaming(t *testing.T) {
	var buf bytes.Buffer
	long := strings.Repeat("长", 40)
	dup := strings.Repeat("N", 20)
	tables := []Table{
		{Sheet: "Same", Headers: []string{"A"}},
		{Sheet: "same", Headers: []string{"A"}},
		{Sheet: dup, Headers: []string{"A"}},
		{Sheet: dup, Headers: []string{"A"}},
		{Sheet: long + "/?*[]:", Headers: []string{"A"}},
		{Sheet: "History", Headers: []string{"A"}},
		{Sheet: "  ", Headers: []string{"A"}},
	}
	if err := ExportXLSX(&buf, tables); err != nil {
		t.Fatalf("ExportXLSX: %v", err)
	}
	if err := ExportXLS(&buf, tables); err != nil {
		t.Fatalf("ExportXLS: %v", err)
	}
}

func TestXLSXColumnWidthEstimate(t *testing.T) {
	rows := make([][]string, 250)
	for i := range rows {
		rows[i] = []string{"x"}
	}
	var buf bytes.Buffer
	longHeader := strings.Repeat("H", 100)
	if err := ExportXLSX(&buf, []Table{{
		Sheet:   "S",
		Headers: []string{longHeader},
		Rows:    rows,
	}}); err != nil {
		t.Fatalf("ExportXLSX: %v", err)
	}
	if buf.Len() == 0 {
		t.Fatal("empty output")
	}
}

func TestXLSXReadEdgeCases(t *testing.T) {
	// 空表头 + 空数据行。
	var buf bytes.Buffer
	if err := ExportXLSX(&buf, []Table{{
		Sheet:   "E",
		Headers: []string{"", ""},
		Rows:    [][]string{{"", ""}, {"a", "b", "c"}},
	}}); err != nil {
		t.Fatalf("ExportXLSX: %v", err)
	}
	tables, err := Read(bytes.NewReader(buf.Bytes()), "e.xlsx",
		WithHeaderRow(0), WithMaxCols(2), WithTrim(false))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(tables) != 1 {
		t.Fatalf("tables = %d", len(tables))
	}
	table := tables[0]
	if len(table.Headers) != 2 || table.Headers[0] != "Column1" || table.Headers[1] != "Column2" {
		t.Fatalf("headers = %v", table.Headers)
	}
	if len(table.Rows) != 1 || len(table.Rows[0]) != 2 {
		t.Fatalf("rows = %v", table.Rows)
	}

	// 行数限制。
	buf.Reset()
	if err := ExportXLSX(&buf, sampleTables()); err != nil {
		t.Fatalf("ExportXLSX: %v", err)
	}
	limited, err := Read(bytes.NewReader(buf.Bytes()), "d.xlsx", WithMaxRows(1), WithMaxCols(1))
	if err != nil {
		t.Fatalf("Read limited: %v", err)
	}
	for _, item := range limited {
		if len(item.Rows) > 1 || len(item.Headers) > 1 {
			t.Fatalf("limit not applied: %+v", item)
		}
	}
}

func TestReadInputValidation(t *testing.T) {
	if _, err := Read(nil, "a.xlsx"); err == nil {
		t.Fatal("expected nil reader error")
	}
	boom := errors.New("boom")
	if _, err := Read(failReader{err: boom}, "a.xlsx"); !errors.Is(err, boom) {
		t.Fatalf("Read error = %v, want boom", err)
	}
	if _, err := Read(bytes.NewReader([]byte("0123456789")), "a.xlsx", WithMaxBytes(4)); err == nil {
		t.Fatal("expected max bytes error")
	}
	_ = WithMaxBytes(10)
	_ = WithMaxBytes(0)
	_ = WithTrim(true)
	if _, err := Read(bytes.NewReader(nil), "a.xlsx"); err == nil {
		t.Fatal("expected empty file error")
	}
	if _, err := Read(bytes.NewReader([]byte("plain text")), "a.txt"); !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("unsupported = %v", err)
	}
	if _, err := Read(bytes.NewReader([]byte("plain text")), "a.xls"); err == nil {
		t.Fatal("expected unrecognized xls error")
	}
	if _, err := Read(bytes.NewReader([]byte("<not-excel>")), "a.xlsx"); err == nil {
		t.Fatal("expected xlsx parse error")
	}
	// 无扩展名但内容为 SpreadsheetML，且长度超过 512 触发 min 的另一个分支。
	xmlData := []byte(`<?xml version="1.0"?><Workbook><Worksheet ss:Name="X"><Table><Row><Cell><Data>` +
		strings.Repeat("value", 200) + `</Data></Cell></Row></Table></Worksheet></Workbook>`)
	if _, err := Read(bytes.NewReader(xmlData), "noext"); err != nil {
		t.Fatalf("Read xml without ext: %v", err)
	}
	// 前缀不是 <?xml，走 xml 嗅探且数据长度超过 512。
	mixedXML := append([]byte("x<Workbook>"), bytes.Repeat([]byte{' '}, 600)...)
	mixedXML = append(mixedXML, []byte("</Workbook>")...)
	_, _ = Read(bytes.NewReader(mixedXML), "mixed.bin")
}

func TestExtractXLRowEdgeCases(t *testing.T) {
	// nil sheet 触发第三方库 panic，由 recover 吞掉。
	if cells := extractXLRow(nil, 0); cells != nil {
		t.Fatalf("cells = %v, want nil", cells)
	}

	data := readSampleXLS(t)
	book, err := xls.OpenReader(bytes.NewReader(data), "utf-8")
	if err != nil {
		t.Fatalf("open xls: %v", err)
	}
	sheet := book.GetSheet(0)
	if sheet == nil {
		t.Fatal("missing sheet")
	}
	if cells := extractXLRow(sheet, int(sheet.MaxRow)+100); cells != nil {
		t.Fatalf("out of range cells = %v, want nil", cells)
	}
}

func TestHeaderStyleDefaults(t *testing.T) {
	file := excelize.NewFile()
	defer func() { _ = file.Close() }()
	if _, err := headerStyle(file, ExportOptions{}); err != nil {
		t.Fatalf("headerStyle defaults: %v", err)
	}
	if _, err := headerStyle(file, ExportOptions{HeaderFill: "112233", HeaderFontColor: "FFFFFF"}); err != nil {
		t.Fatalf("headerStyle explicit: %v", err)
	}
}

func TestExportXLSXInvalidSheetName(t *testing.T) {
	var buf bytes.Buffer
	if err := ExportXLSX(&buf, []Table{{Sheet: "'bad", Headers: []string{"A"}}}); err == nil {
		t.Fatal("expected invalid first sheet name error")
	}
	if err := ExportXLSX(&buf, []Table{
		{Sheet: "ok", Headers: []string{"A"}},
		{Sheet: "'bad", Headers: []string{"A"}},
	}); err == nil {
		t.Fatal("expected invalid sheet name error")
	}
}

func TestFinalizeTableMaxCols(t *testing.T) {
	table := finalizeTable("S",
		[]string{"a", "b", "c"},
		[][]string{{"1", "2", "3"}},
		ImportOptions{MaxCols: 2, HeaderRow: 1, Trim: true},
	)
	if len(table.Headers) != 2 || len(table.Rows) != 1 || len(table.Rows[0]) != 2 {
		t.Fatalf("finalizeTable = %+v", table)
	}
}
