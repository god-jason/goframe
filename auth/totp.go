package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	// DefaultTOTPDigits 是默认验证码位数。
	DefaultTOTPDigits = 6
	// DefaultTOTPPeriod 是默认时间步长（秒）。
	DefaultTOTPPeriod = 30
	// totpSecretSize 是 TOTP 密钥字节数。
	totpSecretSize = 20
	// recoveryAlphabet 是恢复码字符集，去除易混淆字符后恰好 32 个。
	recoveryAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	// recoveryCodeLength 是恢复码字符数（不含分隔符）。
	recoveryCodeLength = 8
)

// GenerateTOTPSecret 生成 20 字节随机密钥，并以无填充 Base32 返回。
func GenerateTOTPSecret() (string, error) {
	buf := make([]byte, totpSecretSize)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("auth: generate totp secret: %w", err)
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buf), nil
}

// TOTPCode 按 RFC 6238（HMAC-SHA1）计算指定时间的验证码。
// digits <= 0 时使用 6 位，period <= 0 时使用 30 秒。
func TOTPCode(secret string, at time.Time, digits, period int) (string, error) {
	key, err := decodeTOTPSecret(secret)
	if err != nil {
		return "", err
	}
	if digits <= 0 {
		digits = DefaultTOTPDigits
	}
	if digits > 10 {
		return "", fmt.Errorf("auth: totp digits must be between 1 and 10")
	}
	if period <= 0 {
		period = DefaultTOTPPeriod
	}
	unix := at.Unix()
	if unix < 0 {
		return "", fmt.Errorf("auth: totp time must not be before unix epoch")
	}
	return hotpCode(key, uint64(unix/int64(period)), digits), nil
}

// VerifyTOTP 校验验证码，允许前后各 skew 个时间步长的时钟偏差。
// 使用常量时间比较，密钥或参数非法时返回 false。
func VerifyTOTP(secret, code string, at time.Time, digits, period, skew int) bool {
	if digits <= 0 {
		digits = DefaultTOTPDigits
	}
	if digits > 10 {
		return false
	}
	if period <= 0 {
		period = DefaultTOTPPeriod
	}
	if skew < 0 {
		skew = 0
	}
	if skew > 10 {
		skew = 10
	}

	key, err := decodeTOTPSecret(secret)
	if err != nil {
		return false
	}
	unix := at.Unix()
	if unix < 0 {
		return false
	}
	code = strings.TrimSpace(code)
	if len(code) != digits {
		return false
	}

	counter := int64(unix / int64(period))
	for offset := -skew; offset <= skew; offset++ {
		current := counter + int64(offset)
		if current < 0 {
			continue
		}
		expected := hotpCode(key, uint64(current), digits)
		if subtle.ConstantTimeCompare([]byte(expected), []byte(code)) == 1 {
			return true
		}
	}
	return false
}

// OTPAuthURL 生成 otpauth:// 协议 URL，供认证器扫码导入。
func OTPAuthURL(issuer, account, secret string, digits, period int) string {
	if digits <= 0 {
		digits = DefaultTOTPDigits
	}
	if period <= 0 {
		period = DefaultTOTPPeriod
	}

	label := account
	if issuer != "" {
		label = issuer + ":" + account
	}

	values := url.Values{}
	values.Set("secret", strings.TrimRight(sanitizeTOTPSecret(secret), "="))
	values.Set("algorithm", "SHA1")
	values.Set("digits", strconv.Itoa(digits))
	values.Set("period", strconv.Itoa(period))
	if issuer != "" {
		values.Set("issuer", issuer)
	}
	return "otpauth://totp/" + url.PathEscape(label) + "?" + values.Encode()
}

// GenerateRecoveryCodes 生成 n 个一次性恢复码，格式为 XXXX-XXXX。
func GenerateRecoveryCodes(n int) ([]string, error) {
	if n <= 0 {
		return nil, fmt.Errorf("auth: recovery code count must be greater than 0")
	}
	if n > 1000 {
		return nil, fmt.Errorf("auth: recovery code count must not exceed 1000")
	}

	codes := make([]string, 0, n)
	for i := 0; i < n; i++ {
		code, err := randomRecoveryCode()
		if err != nil {
			return nil, err
		}
		codes = append(codes, code)
	}
	return codes, nil
}

// NormalizeRecoveryCode 规范化恢复码：转大写并移除空格、连字符等非字母数字字符。
func NormalizeRecoveryCode(code string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r - ('a' - 'A')
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		default:
			return -1
		}
	}, code)
}

// hotpCode 实现 RFC 4226 动态截断。
func hotpCode(key []byte, counter uint64, digits int) string {
	var counterBytes [8]byte
	binary.BigEndian.PutUint64(counterBytes[:], counter)

	mac := hmac.New(sha1.New, key)
	mac.Write(counterBytes[:])
	sum := mac.Sum(nil)

	offset := sum[len(sum)-1] & 0x0f
	value := (uint32(sum[offset])&0x7f)<<24 |
		uint32(sum[offset+1])<<16 |
		uint32(sum[offset+2])<<8 |
		uint32(sum[offset+3])

	modulus := uint64(1)
	for i := 0; i < digits; i++ {
		modulus *= 10
	}
	code := uint64(value) % modulus
	return fmt.Sprintf("%0*d", digits, code)
}

func randomRecoveryCode() (string, error) {
	raw := make([]byte, recoveryCodeLength)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("auth: generate recovery code: %w", err)
	}
	// 字符集长度为 32，直接取低 5 位即可保持均匀分布。
	code := make([]byte, recoveryCodeLength)
	for i, value := range raw {
		code[i] = recoveryAlphabet[value&31]
	}
	return string(code[:4]) + "-" + string(code[4:]), nil
}

func decodeTOTPSecret(secret string) ([]byte, error) {
	normalized := sanitizeTOTPSecret(secret)
	if normalized == "" {
		return nil, fmt.Errorf("auth: totp secret must not be empty")
	}
	if key, err := base32.StdEncoding.DecodeString(normalized); err == nil {
		return key, nil
	}
	if key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.TrimRight(normalized, "=")); err == nil {
		return key, nil
	}
	return nil, fmt.Errorf("auth: invalid totp secret")
}

func sanitizeTOTPSecret(secret string) string {
	secret = strings.ToUpper(strings.TrimSpace(secret))
	secret = strings.ReplaceAll(secret, " ", "")
	secret = strings.ReplaceAll(secret, "-", "")
	return secret
}
