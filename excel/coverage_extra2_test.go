package excel

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"strings"
	"testing"
)

// 说明：以下残留块经分析在本环境逻辑不可达，保留源码并在此注释：
//   - xls.go:124（GetSheet 返回 nil）：extrame/xls 的 NumSheets 与 sheets 切片长度一致，
//     循环下标恒在范围内，GetSheet 不可能返回 nil。
//   - xls.go:155（Row 返回 nil）：extrame/xls 的 WorkSheet.Row 对缺失行直接对 nil 指针赋值
//     触发 panic（由 extractXLRow 的 recover 吞掉），不会返回 nil。
//   - xls.go:159（LastCol 返回负数）：LastCol 来自 uint16 字段，恒 >= 0。
//   - xlsx.go:83/111（NewStyle 失败）：excelize.NewStyle 仅做内部 XML 生成，不会失败。
//   - xlsx.go:87（SetCellStyle 失败）：要触发无效区域需 width>16384，但彼时 SetCellValue
//     已先行失败返回，本行不可达。
//   - xlsx.go:101（lastRow<1）：进入该分支时必已有表头行，row>=2，lastRow>=1。
//   - xlsx.go:36（WriteToBuffer 失败）：excelize v2.9.1 对控制字符/非法 UTF-8 做静默清洗，
//     经探测（\x00、\xff\xfe、代理对、超长值）均不报错，无法从 ExportXLSX 入参触发。
//   - xlsx.go:329（rows.Error 非空）：excelize v2.9.1 的 Rows.err 字段从未被赋值，
//     Error() 恒返回 nil；损坏 XML 在 Next() 中被当作流结束静默截断。

// coverageZipRewrite 将 xlsx 字节解包为条目映射，应用 transform 后重新打包。
func coverageZipRewrite(t *testing.T, data []byte, transform func(entries map[string][]byte)) []byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	entries := map[string][]byte{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open entry %s: %v", f.Name, err)
		}
		content, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("read entry %s: %v", f.Name, err)
		}
		entries[f.Name] = content
	}
	transform(entries)

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create entry %s: %v", name, err)
		}
		if _, err := w.Write(content); err != nil {
			t.Fatalf("write entry %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

// TestCoverageApplyImportOptionsZeroMaxBytes 覆盖 MaxBytes 被置为非正数时的默认值兜底分支。
func TestCoverageApplyImportOptionsZeroMaxBytes(t *testing.T) {
	options := applyImportOptions([]ImportOption{func(o *ImportOptions) { o.MaxBytes = 0 }})
	if options.MaxBytes != 64<<20 {
		t.Fatalf("MaxBytes = %d", options.MaxBytes)
	}
}

// TestCoverageExportXLSXOverColumnLimit 覆盖超出列上限时 SetCellValue 失败并向上传播的路径。
func TestCoverageExportXLSXOverColumnLimit(t *testing.T) {
	headers := make([]string, 16385)
	for i := range headers {
		headers[i] = fmt.Sprintf("h%d", i)
	}
	var buf bytes.Buffer
	err := ExportXLSX(&buf, []Table{{Sheet: "wide", Headers: headers}})
	if err == nil {
		t.Fatal("expected error for over-limit headers")
	}

	// 数据行超列限制，覆盖行单元写入失败分支。
	rows := [][]string{make([]string, 16385)}
	for i := range rows[0] {
		rows[0][i] = fmt.Sprintf("c%d", i)
	}
	buf.Reset()
	err = ExportXLSX(&buf, []Table{{Sheet: "wide2", Rows: rows}})
	if err == nil {
		t.Fatal("expected error for over-limit row cells")
	}
}

// TestCoverageExportXLSXZebraWithoutHeaders 覆盖无表头时斑马纹起始行兜底分支。
func TestCoverageExportXLSXZebraWithoutHeaders(t *testing.T) {
	var buf bytes.Buffer
	tables := []Table{{Sheet: "zebra", Rows: [][]string{{"a", "b"}, {"c", "d"}}}}
	if err := ExportXLSX(&buf, tables, WithZebra(true)); err != nil {
		t.Fatalf("export zebra: %v", err)
	}
	round, err := Read(bytes.NewReader(buf.Bytes()), "zebra.xlsx")
	if err != nil {
		t.Fatalf("read zebra: %v", err)
	}
	// 默认导入把首行识别为表头，导出内容应完整回读。
	if len(round) != 1 || len(round[0].Headers) != 2 || len(round[0].Rows) != 1 {
		t.Fatalf("round trip = %+v", round)
	}
}

// TestCoverageReadXLSXMissingSheetPart 覆盖工作表 XML 部件缺失时 Rows 失败的路径。
func TestCoverageReadXLSXMissingSheetPart(t *testing.T) {
	var buf bytes.Buffer
	if err := ExportXLSX(&buf, []Table{{Sheet: "gone", Headers: []string{"a"}, Rows: [][]string{{"1"}}}}); err != nil {
		t.Fatalf("export: %v", err)
	}
	data := coverageZipRewrite(t, buf.Bytes(), func(entries map[string][]byte) {
		delete(entries, "xl/worksheets/sheet1.xml")
	})
	if _, err := Read(bytes.NewReader(data), "gone.xlsx"); err == nil {
		t.Fatal("expected error for missing sheet part")
	}
}

// TestCoverageReadXLSXInvalidCellRef 覆盖单元格引用非法导致 Columns 失败的路径。
func TestCoverageReadXLSXInvalidCellRef(t *testing.T) {
	var buf bytes.Buffer
	if err := ExportXLSX(&buf, []Table{{Sheet: "ref", Headers: []string{"a"}, Rows: [][]string{{"1"}}}}); err != nil {
		t.Fatalf("export: %v", err)
	}
	broken := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>` +
		`<row r="1"><c r="INVALID"><v>1</v></c></row></sheetData></worksheet>`
	data := coverageZipRewrite(t, buf.Bytes(), func(entries map[string][]byte) {
		entries["xl/worksheets/sheet1.xml"] = []byte(broken)
	})
	if _, err := Read(bytes.NewReader(data), "ref.xlsx"); err == nil {
		t.Fatal("expected error for invalid cell reference")
	}
}

// TestCoverageExportXLSXWriteToBufferError 探测 excelize WriteToBuffer 失败路径。
// 若当前 excelize 版本对非法字符做静默清洗而非报错，则跳过实际断言。
func TestCoverageExportXLSXWriteToBufferError(t *testing.T) {
	candidates := []string{"bad\x00value", "\xff\xfe", "\xed\xa0\x80", strings.Repeat("x", 40000)}
	for _, v := range candidates {
		var buf bytes.Buffer
		if err := ExportXLSX(&buf, []Table{{Sheet: "probe", Headers: []string{"a"}, Rows: [][]string{{v}}}}); err != nil {
			t.Logf("WriteToBuffer error triggered by %q: %v", v, err)
			return
		}
	}
	// 该版本 excelize 清洗了非法字符，WriteToBuffer 不报错。
	t.Log("excelize v2.9.1 sanitizes invalid chars; WriteToBuffer error branch not triggered")
}

// TestCoverageReadBIFFEmptySheetName 通过把 BOUNDSHEET 记录名称长度改为 0，
// 覆盖 readBIFF 中空工作表名兜底分支。
func TestCoverageReadBIFFEmptySheetName(t *testing.T) {
	data := readSampleXLS(t)
	tables, err := Read(bytes.NewReader(data), "Table.xls")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	if len(tables) == 0 || tables[0].Sheet == "" {
		t.Fatalf("fixture sheets = %+v", tables)
	}
	name := tables[0].Sheet

	patched := append([]byte(nil), data...)
	found := false
	for i := 0; i+8 < len(patched); i++ {
		// 记录头：id=0x0085（小端 85 00），size 为记录体长度。
		if patched[i] != 0x85 || patched[i+1] != 0x00 {
			continue
		}
		size := int(binary.LittleEndian.Uint16(patched[i+2 : i+4]))
		body := i + 4
		if size < 8 || body+size > len(patched) {
			continue
		}
		cch := int(patched[body+6])
		flag := patched[body+7]
		if cch != len(name) {
			continue
		}
		// 校验名称字节与首次读取结果一致，避免误改其他记录。
		match := false
		if flag&0x1 == 0 {
			got := string(patched[body+8 : body+8+cch])
			match = got == name
		} else {
			chars := make([]byte, 0, cch)
			for k := 0; k < cch && body+8+2*k+1 < len(patched); k++ {
				chars = append(chars, patched[body+8+2*k])
			}
			match = string(chars) == name
		}
		if !match {
			continue
		}
		patched[body+6] = 0
		found = true
		break
	}
	if !found {
		t.Skipf("BOUNDSHEET record for name %q not located in fixture", name)
	}

	patchedTables, err := Read(bytes.NewReader(patched), "Table.xls")
	if err != nil {
		t.Fatalf("read patched: %v", err)
	}
	if len(patchedTables) == 0 || patchedTables[0].Sheet != "Sheet1" {
		t.Fatalf("patched sheets = %+v", patchedTables)
	}
}
