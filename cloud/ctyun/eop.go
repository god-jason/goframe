package ctyun

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/url"
	"sort"
	"strings"
	"time"
)

// eopTimeLayout 是 EOP 请求时间的 UTC 格式。
const eopTimeLayout = "20060102T150405Z"

// nowFunc 便于测试固定签名时间。
var nowFunc = time.Now

// eopSign 计算天翼云 EOP 平台签名（AK/SK + HMAC-SHA256 派生密钥）。
// requestID 为请求流水号（uuid），now 为请求时间（按 UTC 取值）。
// 返回 eop-date 头与 Eop-Authorization 头的值，签名头固定为
// ctyun-eop-request-id 与 eop-date 两个。
func eopSign(ak, sk, requestID string, u *url.URL, body []byte, now time.Time) (eopDate, authorization string) {
	eopDate = now.UTC().Format(eopTimeLayout)

	// 待签字符串：排序的签名头 + 排序的 query + body 的 sha256 摘要。
	headers := "ctyun-eop-request-id:" + requestID + "\neop-date:" + eopDate + "\n"
	sigture := headers + "\n" + sortedQuery(u) + "\n" + sha256Hex(body)

	// 派生密钥：kTime(sk, eop-date) -> kAk(ak) -> kDate(yyyymmdd)。
	kTime := hmacSHA256([]byte(sk), []byte(eopDate))
	kAk := hmacSHA256(kTime, []byte(ak))
	kDate := hmacSHA256(kAk, []byte(eopDate[:8]))
	signature := base64.StdEncoding.EncodeToString(hmacSHA256(kDate, []byte(sigture)))

	authorization = ak + " Headers=ctyun-eop-request-id;eop-date Signature=" + signature
	return eopDate, authorization
}

// sortedQuery 返回按字典序排序后的查询串。
func sortedQuery(u *url.URL) string {
	if u == nil || u.RawQuery == "" {
		return ""
	}
	parts := strings.Split(u.RawQuery, "&")
	sort.Strings(parts)
	return strings.Join(parts, "&")
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func hmacSHA256(key, data []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}
