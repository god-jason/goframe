package license

import (
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Options 是许可管理器的构造参数。
type Options struct {
	// PublicKey 是校验许可签名的 Ed25519 公钥。
	PublicKey ed25519.PublicKey
	// LicenseFile 是许可文件路径。
	LicenseFile string
	// LicenseData 为许可内容；非空时优先于 LicenseFile，便于测试或远程下发。
	LicenseData []byte
	// Fingerprint 是当前机器指纹，用于机器绑定。
	Fingerprint string
	// Revocation 是离线吊销列表，可为空。
	Revocation *RevocationList
	// StateFile 用于记录最近一次校验时间并检测时钟回拨；为空则不持久化。
	StateFile string
	// Now 可注入时钟，默认 time.Now。
	Now func() time.Time
	// ClockTolerance 是允许的时钟误差，默认 5 分钟。
	ClockTolerance time.Duration
}

// Manager 负责加载、校验并对外提供许可状态与授权判断。
type Manager struct {
	mu   sync.RWMutex
	opts Options

	license  *License
	status   Status
	reason   string
	lastSeen time.Time
}

// stateData 是反回拨状态文件内容。
type stateData struct {
	LicenseID string    `json:"license_id"`
	LastSeen  time.Time `json:"last_seen"`
}

// New 加载并校验许可。签名或格式错误返回 error；
// 过期、吊销、机器不匹配等业务状态通过 Snapshot/Status 暴露。
func New(options Options) (*Manager, error) {
	if len(options.PublicKey) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("license: invalid public key")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.ClockTolerance <= 0 {
		options.ClockTolerance = 5 * time.Minute
	}
	if options.LicenseFile == "" && len(options.LicenseData) == 0 {
		return nil, fmt.Errorf("license: license file is required")
	}

	manager := &Manager{opts: options}
	if err := manager.load(); err != nil {
		return nil, err
	}
	return manager, nil
}

// Reload 重新读取并校验许可文件，失败时保留旧状态。
func (m *Manager) Reload() error {
	if m == nil {
		return fmt.Errorf("license: manager is nil")
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	license, status, reason, err := m.evaluateLocked()
	if err != nil {
		return err
	}
	m.license = license
	m.status = status
	m.reason = reason
	return nil
}

// Snapshot 返回当前许可状态快照。
func (m *Manager) Snapshot() Snapshot {
	if m == nil {
		return Snapshot{Status: StatusExpired, Reason: "license is not loaded"}
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return Snapshot{
		Status:  m.status,
		Valid:   m.status.Valid(),
		InGrace: m.status == StatusGrace,
		Reason:  m.reason,
		License: m.license.Clone(),
	}
}

// Status 返回当前许可状态。
func (m *Manager) Status() Status {
	if m == nil {
		return StatusExpired
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.status
}

// Valid 判断许可当前是否可用（含宽限期）。
func (m *Manager) Valid() bool {
	return m.Status().Valid()
}

// InGrace 判断许可是否处于宽限期。
func (m *Manager) InGrace() bool {
	return m.Status() == StatusGrace
}

// License 返回当前许可副本。
func (m *Manager) License() *License {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.license.Clone()
}

// HasFeature 判断当前许可是否包含指定功能。
func (m *Manager) HasFeature(feature string) bool {
	if m == nil {
		return false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if !m.status.Valid() {
		return false
	}
	return m.license.HasFeature(feature)
}

// Limit 返回配额；第二个返回值表示是否配置了该配额。
func (m *Manager) Limit(key string) (int64, bool) {
	if m == nil {
		return 0, false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if !m.status.Valid() {
		return 0, false
	}
	return m.license.Limit(key)
}

// WithinLimit 判断在新增之前是否仍在配额内。
// 未配置的配额与 Unlimited 均视为不限制。
func (m *Manager) WithinLimit(key string, current int64) bool {
	limit, ok := m.Limit(key)
	if !ok || limit < 0 {
		return true
	}
	return current < limit
}

// load 首次加载并更新内部状态。
func (m *Manager) load() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	license, status, reason, err := m.evaluateLocked()
	if err != nil {
		return err
	}
	m.license = license
	m.status = status
	m.reason = reason
	return nil
}

// evaluateLocked 读取许可、校验签名、检测回拨并写入状态。
// 调用方必须持有写锁。
func (m *Manager) evaluateLocked() (*License, Status, string, error) {
	data := m.opts.LicenseData
	if len(data) == 0 {
		content, err := os.ReadFile(m.opts.LicenseFile)
		if err != nil {
			return nil, "", "", fmt.Errorf("license: read license: %w", err)
		}
		data = content
	}

	license, err := VerifyLicense(data, m.opts.PublicKey)
	if err != nil {
		return nil, "", "", err
	}

	now := m.opts.Now()
	status, reason := Evaluate(license, now, m.opts.Fingerprint, m.opts.Revocation)

	state, exists, err := m.readState()
	if err != nil {
		return nil, "", "", err
	}
	rollback := false
	if exists && state.LicenseID == license.ID {
		if now.Add(m.opts.ClockTolerance).Before(state.LastSeen) {
			rollback = true
		}
	}
	if rollback {
		status = StatusClockRollback
		reason = "system clock moved backwards"
	}

	m.lastSeen = now
	if exists && state.LastSeen.After(now) {
		m.lastSeen = state.LastSeen
	}
	if !rollback {
		if err := m.writeState(stateData{LicenseID: license.ID, LastSeen: m.lastSeen}); err != nil {
			return nil, "", "", fmt.Errorf("license: write state: %w", err)
		}
	}
	return license, status, reason, nil
}

// readState 读取反回拨状态；文件不存在时 exists 为 false。
func (m *Manager) readState() (stateData, bool, error) {
	if m.opts.StateFile == "" {
		return stateData{}, false, nil
	}
	content, err := os.ReadFile(m.opts.StateFile)
	if err != nil {
		if os.IsNotExist(err) {
			return stateData{}, false, nil
		}
		return stateData{}, false, fmt.Errorf("license: read state: %w", err)
	}
	var state stateData
	if err := json.Unmarshal(content, &state); err != nil {
		return stateData{}, false, fmt.Errorf("license: decode state: %w", err)
	}
	return state, true, nil
}

func (m *Manager) writeState(state stateData) error {
	if m.opts.StateFile == "" {
		return nil
	}
	dir := filepath.Dir(m.opts.StateFile)
	if dir == "" {
		dir = "."
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	content, err := json.Marshal(state)
	if err != nil {
		return err
	}

	// 先写临时文件再原子替换，避免进程中断留下损坏的状态文件。
	temp, err := os.CreateTemp(dir, ".license-state-*")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer func() { _ = os.Remove(tempName) }()

	if _, err := temp.Write(content); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempName, m.opts.StateFile)
}
