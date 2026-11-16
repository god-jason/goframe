package excel

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func sampleTables() []Table {
	return []Table{
		{
			Sheet:   "用户",
			Headers: []string{"ID", "姓名", "状态"},
			Rows: [][]string{
				{"1", "张三", "启用"},
				{"2", "李四", "禁用"},
			},
		},
		{
			Sheet:   "设备",
			Headers: []string{"设备号", "名称"},
			Rows: [][]string{
				{"D-001", "温度计"},
			},
		},
	}
}

func assertTablesEqual(t *testing.T, got, want []Table) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("tables = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Sheet != want[i].Sheet {
			t.Fatalf("sheet[%d] = %q, want %q", i, got[i].Sheet, want[i].Sheet)
		}
		if strings.Join(got[i].Headers, "|") != strings.Join(want[i].Headers, "|") {
			t.Fatalf("headers[%d] = %v, want %v", i, got[i].Headers, want[i].Headers)
		}
		if len(got[i].Rows) != len(want[i].Rows) {
			t.Fatalf("rows[%d] = %d, want %d", i, len(got[i].Rows), len(want[i].Rows))
		}
		for r := range want[i].Rows {
			if strings.Join(got[i].Rows[r], "|") != strings.Join(want[i].Rows[r], "|") {
				t.Fatalf("row[%d][%d] = %v, want %v", i, r, got[i].Rows[r], want[i].Rows[r])
			}
		}
	}
}

func TestXLSXRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if err := ExportXLSX(&buf, sampleTables(), WithZebra(true), WithColumnWidths(10, 20, 12)); err != nil {
		t.Fatalf("ExportXLSX: %v", err)
	}
	if buf.Len() == 0 {
		t.Fatal("empty xlsx")
	}

	tables, err := Read(bytes.NewReader(buf.Bytes()), "data.xlsx")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	assertTablesEqual(t, tables, sampleTables())
}

func TestSpreadsheetMLRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if err := ExportXLS(&buf, sampleTables()); err != nil {
		t.Fatalf("ExportXLS: %v", err)
	}
	if !bytes.Contains(buf.Bytes(), []byte("<Workbook")) {
		t.Fatal("expected SpreadsheetML output")
	}

	tables, err := Read(bytes.NewReader(buf.Bytes()), "data.xls")
	if err != nil {
		t.Fatalf("Read xls: %v", err)
	}
	assertTablesEqual(t, tables, sampleTables())
}

func TestExportDispatch(t *testing.T) {
	var buf bytes.Buffer
	if err := Export(&buf, "data.xlsx", sampleTables()); err != nil {
		t.Fatalf("Export xlsx: %v", err)
	}
	if err := Export(&buf, "data.xls", sampleTables()); err != nil {
		t.Fatalf("Export xls: %v", err)
	}
	if err := Export(&buf, "data.csv", sampleTables()); !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("Export csv = %v, want ErrUnsupportedFormat", err)
	}
	if err := Export(&buf, "data.xlsx", nil); err == nil {
		t.Fatal("expected empty tables error")
	}
}

func TestReadHeaderRowAndLimits(t *testing.T) {
	tables := []Table{{
		Sheet: "S",
		Rows: [][]string{
			{"报表标题"},
			{"ID", "名称", "备注"},
			{"1", "A", "x"},
			{"2", "B", "y"},
		},
	}}
	var buf bytes.Buffer
	if err := ExportXLSX(&buf, tables); err != nil {
		t.Fatalf("ExportXLSX: %v", err)
	}

	got, err := Read(bytes.NewReader(buf.Bytes()), "a.xlsx", WithHeaderRow(2), WithMaxRows(1), WithMaxCols(2))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("tables = %d", len(got))
	}
	if strings.Join(got[0].Headers, "|") != "ID|名称" {
		t.Fatalf("headers = %v", got[0].Headers)
	}
	if len(got[0].Rows) != 1 || len(got[0].Rows[0]) != 2 {
		t.Fatalf("rows = %v", got[0].Rows)
	}
	if got[0].Rows[0][0] != "1" || got[0].Rows[0][1] != "A" {
		t.Fatalf("row = %v", got[0].Rows[0])
	}
}

func TestReadSheetSelection(t *testing.T) {
	var buf bytes.Buffer
	if err := ExportXLSX(&buf, sampleTables()); err != nil {
		t.Fatalf("ExportXLSX: %v", err)
	}
	tables, err := Read(bytes.NewReader(buf.Bytes()), "a.xlsx", WithSheet("设备"))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(tables) != 1 || tables[0].Sheet != "设备" {
		t.Fatalf("unexpected tables: %+v", tables)
	}
	if _, err := Read(bytes.NewReader(buf.Bytes()), "a.xlsx", WithSheet("missing")); err == nil {
		t.Fatal("expected missing sheet error")
	}
}

func TestReadInvalidData(t *testing.T) {
	if _, err := Read(bytes.NewReader([]byte("not an excel file")), "a.xlsx"); err == nil {
		t.Fatal("expected invalid data error")
	}
}
