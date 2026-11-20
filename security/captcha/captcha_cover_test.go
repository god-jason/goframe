package captcha

import (
	"crypto/rand"
	"io"
	"strings"
	"testing"
)

// loopReader 循环吐出固定字节序列，永不返回错误。
// 注意：Go 1.24+ 的 crypto/rand.Read 在自定义 Reader 出错时会 fatal 终止进程，
// 因此只能注入"永不失败"的确定性数据源。
type loopReader struct {
	data []byte
	pos  int
}

func (r *loopReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = r.data[r.pos%len(r.data)]
		r.pos++
	}
	return len(p), nil
}

// TestRandomDigitsSkipsHighBytes 覆盖 randomDigits 中 value>=250 丢弃并 continue 的分支。
func TestRandomDigitsSkipsHighBytes(t *testing.T) {
	original := rand.Reader
	// 模式同时包含 >=250 的高字节（触发 continue）与低位字节（生成数字）。
	rand.Reader = io.Reader(&loopReader{data: []byte{250, 251, 255, 100, 249, 101, 254, 102}})
	defer func() { rand.Reader = original }()

	code, err := randomDigits(6)
	if err != nil {
		t.Fatalf("randomDigits: %v", err)
	}
	if len(code) != 6 {
		t.Fatalf("code len = %d, want 6", len(code))
	}
	if strings.TrimLeft(code, "0123456789") != "" {
		t.Fatalf("code = %q, want digits only", code)
	}
	// 模式里高字节占比 5/8，6 位码必然经历过 continue 分支才凑齐长度。
}

// 说明：captcha.go 中以下分支为逻辑不可达的防御分支，无法在测试中触发：
// 1. randomDigits 的 rand.Read 错误返回（Go 1.24+ 中 crypto/rand.Read 出错直接 fatal，永不返回 error）；
// 2. render 的 png.Encode 错误返回（编码目标为 bytes.Buffer，且图像均为合法 RGBA，标准库不会报错）。
