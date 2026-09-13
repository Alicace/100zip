// Package vault 提供本地加密的密码库（AES-256-GCM）。
//
// 设计要点（对齐 docs/07-权限与安全设计.md）：
//   - 密钥文件 vault.key（0600，仅应用用户可读），首次使用自动生成；
//   - 明文密码**不返回前端**：前端只传 label，由后端在解压/压缩时解析；
//   - 不写入日志。
package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// Item 是密码库条目的公开视图（不含明文）。
type Item struct {
	Label  string `json:"label"`
	Length int    `json:"length"`
}

type record struct {
	Label  string `json:"label"`
	Length int    `json:"length"`
	Nonce  string `json:"nonce"`
	Data   string `json:"data"`
}

// Vault 是密码库。
type Vault struct {
	mu      sync.Mutex
	dir     string
	key     []byte
	records []record
}

// Open 打开（或初始化）密码库。
func Open(dataDir string) (*Vault, error) {
	v := &Vault{dir: dataDir}
	key, err := v.loadOrCreateKey()
	if err != nil {
		return nil, err
	}
	v.key = key
	if data, err := os.ReadFile(v.file()); err == nil {
		_ = json.Unmarshal(data, &v.records)
	}
	return v, nil
}

func (v *Vault) file() string    { return filepath.Join(v.dir, "vault.json") }
func (v *Vault) keyFile() string { return filepath.Join(v.dir, "vault.key") }

func (v *Vault) loadOrCreateKey() ([]byte, error) {
	if data, err := os.ReadFile(v.keyFile()); err == nil && len(data) == 32 {
		return data, nil
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(v.dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(v.keyFile(), key, 0o600); err != nil {
		return nil, err
	}
	return key, nil
}

// List 返回所有条目（仅 label 与长度）。
func (v *Vault) List() []Item {
	v.mu.Lock()
	defer v.mu.Unlock()
	out := make([]Item, 0, len(v.records))
	for _, r := range v.records {
		out = append(out, Item{Label: r.Label, Length: r.Length})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out
}

// Add 新增或更新一个条目。
func (v *Vault) Add(label, password string) error {
	if label == "" || password == "" {
		return errors.New("label 与密码不能为空")
	}
	block, err := aes.NewCipher(v.key)
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	sealed := gcm.Seal(nil, nonce, []byte(password), nil)

	v.mu.Lock()
	replaced := false
	for i := range v.records {
		if v.records[i].Label == label {
			v.records[i] = record{Label: label, Length: len(password),
				Nonce: base64.StdEncoding.EncodeToString(nonce),
				Data:  base64.StdEncoding.EncodeToString(sealed)}
			replaced = true
			break
		}
	}
	if !replaced {
		v.records = append(v.records, record{Label: label, Length: len(password),
			Nonce: base64.StdEncoding.EncodeToString(nonce),
			Data:  base64.StdEncoding.EncodeToString(sealed)})
	}
	v.mu.Unlock()
	return v.persist()
}

// Get 取出明文密码（仅后端内部使用）。
func (v *Vault) Get(label string) (string, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	for _, r := range v.records {
		if r.Label != label {
			continue
		}
		nonce, err1 := base64.StdEncoding.DecodeString(r.Nonce)
		data, err2 := base64.StdEncoding.DecodeString(r.Data)
		if err1 != nil || err2 != nil {
			return "", false
		}
		block, err := aes.NewCipher(v.key)
		if err != nil {
			return "", false
		}
		gcm, err := cipher.NewGCM(block)
		if err != nil {
			return "", false
		}
		plain, err := gcm.Open(nil, nonce, data, nil)
		if err != nil {
			return "", false
		}
		return string(plain), true
	}
	return "", false
}

// Remove 删除一个条目。
func (v *Vault) Remove(label string) error {
	v.mu.Lock()
	kept := v.records[:0]
	for _, r := range v.records {
		if r.Label != label {
			kept = append(kept, r)
		}
	}
	v.records = kept
	v.mu.Unlock()
	return v.persist()
}

func (v *Vault) persist() error {
	v.mu.Lock()
	data, err := json.MarshalIndent(v.records, "", "  ")
	v.mu.Unlock()
	if err != nil {
		return err
	}
	tmp := v.file() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, v.file())
}
