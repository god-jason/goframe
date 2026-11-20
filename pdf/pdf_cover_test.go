package pdf

import (
	"strings"
	"testing"

	"golang.org/x/image/font/gofont/goregular"
)

// TestDefaultFontRegisterFailure 覆盖 New 中默认字体注册失败的分支：
// 临时把内置字体数据替换为非法字节，AddTTFFontData 必然解析失败。
func TestDefaultFontRegisterFailure(t *testing.T) {
	original := goregular.TTF
	goregular.TTF = []byte("not-a-ttf")
	defer func() { goregular.TTF = original }()

	if _, err := New(); err == nil || !strings.Contains(err.Error(), "register default font") {
		t.Fatalf("New err = %v, want register default font error", err)
	}
}

// TestPageBreakWithoutContent 覆盖 finalize 中 SetPage 失败的分支：
// PageBreak 后未向新页写入任何内容，新页没有对应的内容对象，
// finalize 遍历页码时 SetPage(total) 找不到该页而报错。
func TestPageBreakWithoutContent(t *testing.T) {
	doc, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	doc.PageBreak()
	if _, err := doc.Bytes(); err == nil || !strings.Contains(err.Error(), "set page") {
		t.Fatalf("Bytes err = %v, want set page error", err)
	}
}

// TestTableRowLongerThanHeaders 覆盖 tableColumnCount 中行宽大于表头宽时取行宽的分支。
func TestTableRowLongerThanHeaders(t *testing.T) {
	doc, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	table := Table{
		Headers: []string{"A"},
		Rows:    [][]string{{"1", "2", "3"}},
	}
	if err := doc.Table(table); err != nil {
		t.Fatalf("Table: %v", err)
	}
	if _, err := doc.Bytes(); err != nil {
		t.Fatalf("Bytes: %v", err)
	}
}

// 说明：pdf 包中以下分支为逻辑不可达的防御分支，无法在测试中触发：
// 1. Bytes 中 GetBytesPdfReturnErr 的错误返回（gopdf 的 Close/compilePdf 对内存缓冲永不返回错误）；
// 2. MeasureTextWidth / Cell 的错误返回（gopdf v0.33 对合法解析的 TTF 字体，
//    AddChars/createContent 仅产生 ErrGlyphNotFound 并被内部消化，不会向上返回错误）；
// 3. finalize 中恢复当前页 SetPage(total) 的错误返回（循环内已对同页成功，恢复必然成功）。
