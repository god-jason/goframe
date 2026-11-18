// Package license 提供离线签名许可的签发、校验、功能授权与配额控制。
//
// 许可文件由 Ed25519 私钥签发，运行时仅持有公钥即可离线校验，
// 支持有效期、宽限期、功能开关、配额、机器绑定、吊销列表与时钟回拨检测。
package license

import (
	"fmt"
	"strings"
	"time"
)

// Unlimited 表示不限制配额。
const Unlimited int64 = -1

// License 是许可内容。签名后以信封形式保存，运行时不修改。
type License struct {
	ID       string `json:"id"`
	Product  string `json:"product"`
	Customer string `json:"customer,omitempty"`
	Edition  string `json:"edition,omitempty"`
	// IssuedAt 是签发时间。
	IssuedAt time.Time `json:"issued_at"`
	// NotBefore 是生效时间，为空表示立即生效。
	NotBefore *time.Time `json:"not_before,omitempty"`
	// ExpiresAt 是到期时间，为空表示永久有效。
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	// GracePeriodSeconds 是到期后的宽限期，宽限期内仍可用但状态为 grace。
	GracePeriodSeconds int64 `json:"grace_period_seconds,omitempty"`
	// Features 是授权的功能标识，支持 "*" 表示全部功能。
	Features []string `json:"features,omitempty"`
	// Limits 是配额，-1 表示不限制，未配置的 key 视为不限制。
	Limits map[string]int64 `json:"limits,omitempty"`
	// Machines 是允许运行的机器指纹；为空或包含 "*" 表示不绑定机器。
	Machines []string `json:"machines,omitempty"`
	// Metadata 是业务自定义信息，不参与授权判定。
	Metadata map[string]string `json:"metadata,omitempty"`
}

// GracePeriod 返回宽限期。
func (l *License) GracePeriod() time.Duration {
	if l == nil || l.GracePeriodSeconds <= 0 {
		return 0
	}
	return time.Duration(l.GracePeriodSeconds) * time.Second
}

// ExpiresWithGrace 返回到期时间与是否配置了到期时间。
func (l *License) ExpiresWithGrace() (time.Time, bool) {
	if l == nil || l.ExpiresAt == nil {
		return time.Time{}, false
	}
	return l.ExpiresAt.Add(l.GracePeriod()), true
}

// HasFeature 判断是否包含指定功能，"*" 表示全部功能。
func (l *License) HasFeature(feature string) bool {
	if l == nil {
		return false
	}
	feature = strings.TrimSpace(feature)
	for _, item := range l.Features {
		if item == "*" || item == feature {
			return true
		}
	}
	return false
}

// Limit 返回配额；第二个返回值表示是否配置了该配额。
func (l *License) Limit(key string) (int64, bool) {
	if l == nil || l.Limits == nil {
		return 0, false
	}
	value, ok := l.Limits[key]
	return value, ok
}

// Clone 返回深拷贝，避免调用方修改共享许可。
func (l *License) Clone() *License {
	if l == nil {
		return nil
	}
	clone := *l
	clone.Features = append([]string(nil), l.Features...)
	clone.Machines = append([]string(nil), l.Machines...)
	if l.NotBefore != nil {
		t := *l.NotBefore
		clone.NotBefore = &t
	}
	if l.ExpiresAt != nil {
		t := *l.ExpiresAt
		clone.ExpiresAt = &t
	}
	if l.Limits != nil {
		clone.Limits = make(map[string]int64, len(l.Limits))
		for key, value := range l.Limits {
			clone.Limits[key] = value
		}
	}
	if l.Metadata != nil {
		clone.Metadata = make(map[string]string, len(l.Metadata))
		for key, value := range l.Metadata {
			clone.Metadata[key] = value
		}
	}
	return &clone
}

// Status 是许可的运行时状态。
type Status string

const (
	// StatusValid 表示许可有效。
	StatusValid Status = "valid"
	// StatusGrace 表示已过期但仍在宽限期。
	StatusGrace Status = "grace"
	// StatusExpired 表示已过期且超过宽限期。
	StatusExpired Status = "expired"
	// StatusNotYetValid 表示尚未到生效时间。
	StatusNotYetValid Status = "not_yet_valid"
	// StatusRevoked 表示许可已被吊销。
	StatusRevoked Status = "revoked"
	// StatusMachineMismatch 表示当前机器不在授权范围。
	StatusMachineMismatch Status = "machine_mismatch"
	// StatusClockRollback 表示检测到系统时间回拨。
	StatusClockRollback Status = "clock_rollback"
)

// Valid 判断状态是否允许使用授权功能。
func (s Status) Valid() bool {
	return s == StatusValid || s == StatusGrace
}

// Snapshot 是许可状态快照，可直接用于 HTTP 响应。
type Snapshot struct {
	Status  Status   `json:"status"`
	Valid   bool     `json:"valid"`
	InGrace bool     `json:"in_grace"`
	Reason  string   `json:"reason,omitempty"`
	License *License `json:"license,omitempty"`
}

// Evaluate 根据时间、机器指纹与吊销列表评估许可状态。
func Evaluate(license *License, now time.Time, fingerprint string, revocation *RevocationList) (Status, string) {
	if license == nil {
		return StatusExpired, "license is empty"
	}
	if revocation != nil && revocation.Contains(license.ID) {
		return StatusRevoked, "license has been revoked"
	}
	if !machineAllowed(license, fingerprint) {
		return StatusMachineMismatch, "machine fingerprint is not licensed"
	}
	if license.NotBefore != nil && now.Before(*license.NotBefore) {
		return StatusNotYetValid, "license is not valid yet"
	}
	if license.ExpiresAt != nil {
		if now.Before(*license.ExpiresAt) {
			return StatusValid, ""
		}
		if license.GracePeriod() > 0 && now.Before(license.ExpiresAt.Add(license.GracePeriod())) {
			return StatusGrace, "license expired, in grace period"
		}
		return StatusExpired, "license has expired"
	}
	return StatusValid, ""
}

// machineAllowed 判断机器指纹是否在授权范围。
func machineAllowed(license *License, fingerprint string) bool {
	if len(license.Machines) == 0 {
		return true
	}
	fingerprint = strings.TrimSpace(fingerprint)
	for _, item := range license.Machines {
		if item == "*" {
			return true
		}
		if strings.EqualFold(strings.TrimSpace(item), fingerprint) {
			return true
		}
	}
	return false
}

// validate 校验许可的基本字段。
func (l *License) validate() error {
	if strings.TrimSpace(l.ID) == "" {
		return fmt.Errorf("license: id is required")
	}
	if strings.TrimSpace(l.Product) == "" {
		return fmt.Errorf("license: product is required")
	}
	if l.IssuedAt.IsZero() {
		return fmt.Errorf("license: issued_at is required")
	}
	if l.NotBefore != nil && l.ExpiresAt != nil && l.ExpiresAt.Before(*l.NotBefore) {
		return fmt.Errorf("license: expires_at must be after not_before")
	}
	return nil
}
