package sanitize

import (
	"strings"
	"testing"
)

func TestRichTextRemovesScripts(t *testing.T) {
	input := `<p>hello <strong>world</strong></p><script>alert("xss")</script>`
	output := RichText(input)

	if !strings.Contains(output, "<strong>world</strong>") {
		t.Fatalf("expected safe markup to be kept, got %q", output)
	}
	if strings.Contains(output, "script") || strings.Contains(output, "alert") {
		t.Fatalf("expected script to be removed, got %q", output)
	}
}

func TestPlainTextStripsTags(t *testing.T) {
	output := PlainText(`<b>hello</b> <a href="https://example.com">world</a>`)
	if output != "hello world" {
		t.Fatalf("PlainText = %q", output)
	}
	if StripTags(`<i>alias</i>`) != "alias" {
		t.Fatalf("StripTags = %q", StripTags(`<i>alias</i>`))
	}
}

func TestEmptyInput(t *testing.T) {
	if RichText("") != "" || PlainText("") != "" {
		t.Fatal("expected empty output for empty input")
	}
}
