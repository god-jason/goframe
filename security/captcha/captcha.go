// Package captcha 提供图片验证码的生成与一次性校验能力。
package captcha

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	mrand "math/rand/v2"
	"strings"
	"time"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"

	"github.com/god-jason/goframe/id"
)

var (
	// ErrNotFound 表示验证码挑战不存在。
	ErrNotFound = errors.New("captcha: challenge not found")
	// ErrExpired 表示验证码挑战已过期。
	ErrExpired = errors.New("captcha: challenge expired")
	// ErrInvalidCode 表示验证码不匹配。
	ErrInvalidCode = errors.New("captcha: invalid code")
)

// Store 是验证码存储接口，供进程内或分布式实现使用。
type Store interface {
	Set(ctx context.Context, id, code string, ttl time.Duration) error
	Get(ctx context.Context, id string) (string, error)
	Delete(ctx context.Context, id string) error
}

// Config 是验证码配置。
type Config struct {
	// Length 是验证码位数。
	Length int
	// Width、Height 是图片尺寸。
	Width  int
	Height int
	// TTL 是验证码有效期。
	TTL time.Duration
}

// DefaultConfig 返回验证码默认配置。
func DefaultConfig() Config {
	return Config{
		Length: 5,
		Width:  160,
		Height: 60,
		TTL:    5 * time.Minute,
	}
}

// Challenge 是验证码挑战，Image 为 PNG 图片数据。
type Challenge struct {
	ID        string `json:"id"`
	Image     []byte `json:"image"`
	ExpiresIn int64  `json:"expires_in"`
}

// Manager 负责生成与校验验证码。
type Manager struct {
	cfg   Config
	store Store
}

// New 创建验证码管理器。零值字段使用默认配置，store 为空时使用内存存储。
func New(cfg Config, store Store) (*Manager, error) {
	defaults := DefaultConfig()
	if cfg.Length <= 0 {
		cfg.Length = defaults.Length
	}
	if cfg.Width <= 0 {
		cfg.Width = defaults.Width
	}
	if cfg.Height <= 0 {
		cfg.Height = defaults.Height
	}
	if cfg.TTL <= 0 {
		cfg.TTL = defaults.TTL
	}
	if cfg.Length > 10 {
		return nil, fmt.Errorf("captcha: length must be between 1 and 10")
	}
	if cfg.Width < 50 || cfg.Height < 25 {
		return nil, fmt.Errorf("captcha: image size must be at least 50x25")
	}
	if store == nil {
		store = NewMemoryStore()
	}
	return &Manager{cfg: cfg, store: store}, nil
}

// Generate 生成验证码并把答案写入存储。
func (m *Manager) Generate(ctx context.Context) (*Challenge, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	code, err := randomDigits(m.cfg.Length)
	if err != nil {
		return nil, err
	}
	imageData, err := m.render(code)
	if err != nil {
		return nil, err
	}

	challengeID := id.New()
	if err := m.store.Set(ctx, challengeID, code, m.cfg.TTL); err != nil {
		return nil, fmt.Errorf("captcha: save challenge: %w", err)
	}
	return &Challenge{
		ID:        challengeID,
		Image:     imageData,
		ExpiresIn: int64(m.cfg.TTL.Seconds()),
	}, nil
}

// Verify 校验验证码。
// 验证码只能使用一次：无论校验是否成功，读取后都会立即删除。
func (m *Manager) Verify(ctx context.Context, challengeID, code string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	challengeID = strings.TrimSpace(challengeID)
	if challengeID == "" {
		return ErrNotFound
	}

	stored, err := m.store.Get(ctx, challengeID)
	if err != nil {
		_ = m.store.Delete(ctx, challengeID)
		return err
	}
	if err := m.store.Delete(ctx, challengeID); err != nil {
		return fmt.Errorf("captcha: delete challenge: %w", err)
	}

	actual := strings.ToLower(strings.TrimSpace(code))
	if subtle.ConstantTimeCompare([]byte(stored), []byte(actual)) != 1 {
		return ErrInvalidCode
	}
	return nil
}

func randomDigits(length int) (string, error) {
	out := make([]byte, 0, length)
	buf := make([]byte, 32)
	for len(out) < length {
		if _, err := rand.Read(buf); err != nil {
			return "", fmt.Errorf("captcha: generate code: %w", err)
		}
		for _, value := range buf {
			// 拒绝采样，避免取模带来的分布偏差。
			if value >= 250 {
				continue
			}
			out = append(out, '0'+value%10)
			if len(out) == length {
				break
			}
		}
	}
	return string(out), nil
}

func (m *Manager) render(code string) ([]byte, error) {
	width, height := m.cfg.Width, m.cfg.Height
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{
		R: randomByte(235, 255),
		G: randomByte(235, 255),
		B: randomByte(235, 255),
		A: 255,
	}), image.Point{}, draw.Src)

	for i := 0; i < 3; i++ {
		drawLine(img,
			mrand.IntN(width), mrand.IntN(height),
			mrand.IntN(width), mrand.IntN(height),
			color.RGBA{R: randomByte(120, 190), G: randomByte(120, 190), B: randomByte(120, 190), A: 255},
		)
	}
	for i := 0; i < width*height/30; i++ {
		img.Set(mrand.IntN(width), mrand.IntN(height), color.RGBA{
			R: randomByte(110, 200),
			G: randomByte(110, 200),
			B: randomByte(110, 200),
			A: 255,
		})
	}

	textColor := color.RGBA{R: randomByte(20, 80), G: randomByte(20, 80), B: randomByte(20, 80), A: 255}
	text := image.NewRGBA(img.Bounds())
	cell := float64(width) / float64(len(code)+1)
	for i, char := range code {
		x := int(cell*float64(i+1)) - 3 + mrand.IntN(3) - 1
		y := height/2 + 6 + mrand.IntN(7) - 3
		drawer := &font.Drawer{
			Dst:  text,
			Src:  image.NewUniform(textColor),
			Face: basicfont.Face7x13,
			Dot:  fixed.P(x, y),
		}
		drawer.DrawString(string(char))
	}
	rotated := rotateRGBA(text, (mrand.Float64()-0.5)*0.20)
	draw.Draw(img, img.Bounds(), rotated, image.Point{}, draw.Over)

	drawLine(img, 0, mrand.IntN(height), width-1, mrand.IntN(height),
		color.RGBA{R: randomByte(140, 200), G: randomByte(140, 200), B: randomByte(140, 200), A: 255},
	)

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("captcha: encode png: %w", err)
	}
	return buf.Bytes(), nil
}

// rotateRGBA 以图片中心为轴做最近邻旋转，返回带透明背景的新图片。
func rotateRGBA(src *image.RGBA, angle float64) *image.RGBA {
	bounds := src.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, width, height))

	centerX := float64(width) / 2
	centerY := float64(height) / 2
	sin, cos := math.Sincos(angle)

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			dx := float64(x) - centerX
			dy := float64(y) - centerY
			srcX := cos*dx + sin*dy + centerX
			srcY := -sin*dx + cos*dy + centerY
			if srcX < 0 || srcY < 0 || srcX >= float64(width) || srcY >= float64(height) {
				continue
			}
			dst.Set(x, y, src.At(int(srcX), int(srcY)))
		}
	}
	return dst
}

func drawLine(img *image.RGBA, x0, y0, x1, y1 int, c color.Color) {
	dx := abs(x1 - x0)
	dy := -abs(y1 - y0)
	stepX := -1
	if x0 < x1 {
		stepX = 1
	}
	stepY := -1
	if y0 < y1 {
		stepY = 1
	}
	err := dx + dy
	for {
		img.Set(x0, y0, c)
		if x0 == x1 && y0 == y1 {
			return
		}
		e2 := 2 * err
		if e2 >= dy {
			err += dy
			x0 += stepX
		}
		if e2 <= dx {
			err += dx
			y0 += stepY
		}
	}
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func randomByte(min, max int) uint8 {
	return uint8(min + mrand.IntN(max-min+1))
}
