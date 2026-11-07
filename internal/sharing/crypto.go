package sharing

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

type SharedCredentials struct {
	Account  string `json:"account"`
	Password string `json:"password"`
	Operator string `json:"operator"`
	Memo     string `json:"memo,omitempty"`
}

// GenerateRandomKey generates a 256-bit AES key.
func GenerateRandomKey() ([]byte, error) {
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("failed to generate random key: %w", err)
	}
	return key, nil
}

// GenerateShareID generates a random 12-char alphanumeric share ID.
func GenerateShareID() (string, error) {
	b := make([]byte, 9)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", err
	}
	return "sh_" + base64.RawURLEncoding.EncodeToString(b), nil
}

// FormatShareCode packs shareID and AES key into a single string.
// Format: CK1.<share_id>.<base64url_aes_key>
func FormatShareCode(shareID string, key []byte) string {
	return fmt.Sprintf("CK1.%s.%s", shareID, base64.RawURLEncoding.EncodeToString(key))
}

// ParseShareCode parses a CK1 share code string.
func ParseShareCode(code string) (shareID string, key []byte, err error) {
	trimmed := strings.TrimSpace(code)
	parts := strings.Split(trimmed, ".")
	if len(parts) != 3 || parts[0] != "CK1" {
		return "", nil, errors.New("无效的分享码格式（需以 CK1 开头）")
	}
	shareID = parts[1]
	if shareID == "" {
		return "", nil, errors.New("分享码缺少 shareID")
	}
	key, err = base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(key) != 32 {
		return "", nil, errors.New("分享码中密钥损坏或长度不正确")
	}
	return shareID, key, nil
}

// EncryptCredentials encrypts SharedCredentials using AES-256-GCM.
func EncryptCredentials(creds SharedCredentials, key []byte) (string, error) {
	data, err := json.Marshal(creds)
	if err != nil {
		return "", err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}

	ciphertext := gcm.Seal(nonce, nonce, data, nil)
	return base64.RawURLEncoding.EncodeToString(ciphertext), nil
}

// DecryptCredentials decrypts AES-256-GCM ciphertext into SharedCredentials.
func DecryptCredentials(encoded string, key []byte) (*SharedCredentials, error) {
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("密文解码失败: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonceSize := gcm.NonceSize()
	if len(raw) < nonceSize {
		return nil, errors.New("密文数据过短")
	}

	nonce, ciphertext := raw[:nonceSize], raw[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, errors.New("解密凭据失败（密钥错误或数据被篡改）")
	}

	var creds SharedCredentials
	if err := json.Unmarshal(plaintext, &creds); err != nil {
		return nil, fmt.Errorf("凭据数据解析失败: %w", err)
	}

	return &creds, nil
}
