package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"wenchang-campus-keeper/internal/campus"
)

// AppVersion is injected at build time from the root VERSION file via -ldflags.
// Defaults to "dev" during untagged local development.
var AppVersion = "dev"

type Operator string

const (
	OperatorCMCC    Operator = "cmcc"
	OperatorTelecom Operator = "telecom"
	OperatorUnicom  Operator = "unicom"
	OperatorCampus  Operator = "campus"
)

type ActiveMode string

const (
	ModeSelf   ActiveMode = "self"
	ModeFriend ActiveMode = "friend"
)

type FriendShareItem struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	ShareCode string `json:"share_code"`
	Operator  string `json:"operator,omitempty"`
}

type PublishedShareItem struct {
	ShareID   string `json:"share_id"`
	Memo      string `json:"memo"`
	CreatedAt int64  `json:"created_at"`
	ExpiresAt int64  `json:"expires_at"`
}

func (op Operator) DisplayName() string {
	switch op {
	case OperatorCMCC:
		return "移动"
	case OperatorTelecom:
		return "电信"
	case OperatorUnicom:
		return "联通"
	case OperatorCampus:
		return "校园网"
	default:
		return "移动"
	}
}

func (op Operator) Suffix() string {
	switch op {
	case OperatorCMCC:
		return "@cmcc"
	case OperatorTelecom:
		return "@dx"
	case OperatorUnicom:
		return "@lt"
	case OperatorCampus:
		return "@xyw"
	default:
		return "@cmcc"
	}
}

type Config struct {
	ActiveMode      ActiveMode           `json:"active_mode,omitempty"`
	Account         string               `json:"account"`
	Password        string               `json:"password"`
	Operator        Operator             `json:"operator"`
	PortalURL       string               `json:"portal_url,omitempty"`
	CampusMode      campus.Campus        `json:"campus_mode,omitempty"`
	AccountSuffix   string               `json:"account_suffix,omitempty"`
	SuffixExplicit  bool                 `json:"suffix_explicit,omitempty"`
	OwnerAdminToken string               `json:"owner_admin_token,omitempty"`
	CloudHubURL     string               `json:"cloud_hub_url,omitempty"`
	FriendShareCode string               `json:"friend_share_code,omitempty"`
	FriendShares    []FriendShareItem    `json:"friend_shares,omitempty"`
	PublishedShares   []PublishedShareItem `json:"published_shares,omitempty"`
	DisableAutoUpdate bool                 `json:"disable_auto_update,omitempty"`
}

type storedConfig struct {
	ActiveMode      ActiveMode           `json:"active_mode,omitempty"`
	Account         string               `json:"account"`
	Password        string               `json:"password"`
	Operator        Operator             `json:"operator"`
	PortalURL       string               `json:"portal_url,omitempty"`
	OwnerAdminToken string               `json:"owner_admin_token,omitempty"`
	CloudHubURL     string               `json:"cloud_hub_url,omitempty"`
	FriendShareCode string               `json:"friend_share_code,omitempty"`
	FriendShares    []FriendShareItem    `json:"friend_shares,omitempty"`
	PublishedShares   []PublishedShareItem `json:"published_shares,omitempty"`
	DisableAutoUpdate bool                 `json:"disable_auto_update,omitempty"`
}

func (c Config) AutoUpdateEnabled() bool {
	return !c.DisableAutoUpdate
}

func (c Config) Valid() bool {
	if c.ActiveMode == ModeFriend {
		return strings.TrimSpace(c.FriendShareCode) != ""
	}
	return strings.TrimSpace(c.Account) != "" && strings.TrimSpace(c.Password) != ""
}

func generateAdminToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (c Config) OperatorName() string {
	if c.Operator != "" {
		return c.Operator.DisplayName()
	}
	return "移动"
}

func (c Config) AccountFor(site campus.Campus) string {
	acc := strings.TrimSpace(c.Account)
	if strings.Contains(acc, "@") {
		return acc
	}
	if c.SuffixExplicit {
		return acc + c.AccountSuffix
	}
	if c.Operator != "" {
		return acc + c.Operator.Suffix()
	}
	return acc + "@xyw"
}

func Mask(s string) string {
	if len(s) <= 4 {
		return "****"
	}
	return s[:2] + "****" + s[len(s)-2:]
}

func AppDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	dir := filepath.Join(home, ".campus-keeper")
	_ = os.MkdirAll(dir, 0700)
	return dir
}

func ConfigPath() string {
	return filepath.Join(AppDataDir(), "config.dat")
}

func LogPath() string {
	return filepath.Join(AppDataDir(), "keeper.log")
}

// atomicWriteFile writes data to a temporary file in the same directory as filename,
// flushes it to disk, and renames it to filename with 0600 permissions, preventing incomplete file corruption.
func atomicWriteFile(filename string, data []byte) error {
	dir := filepath.Dir(filename)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	tmpFile, err := os.CreateTemp(dir, "cfg-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmpFile.Name()
	defer func() {
		_ = os.Remove(tmpName)
	}()

	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}

	return os.Rename(tmpName, filename)
}

func SaveEncryptedConfig(cfg Config) error {
	mode := cfg.ActiveMode
	if mode == "" {
		mode = ModeSelf
	}
	hubURL := cfg.CloudHubURL
	if hubURL == "" {
		hubURL = "https://keeper.egami.online"
	}
	adminToken := cfg.OwnerAdminToken
	if adminToken == "" {
		adminToken = generateAdminToken()
	}
	s := storedConfig{
		ActiveMode:      mode,
		Account:         strings.TrimSpace(cfg.Account),
		Password:        cfg.Password,
		Operator:        cfg.Operator,
		PortalURL:       cfg.PortalURL,
		OwnerAdminToken: adminToken,
		CloudHubURL:     hubURL,
		FriendShareCode: strings.TrimSpace(cfg.FriendShareCode),
		FriendShares:    cfg.FriendShares,
		PublishedShares:   cfg.PublishedShares,
		DisableAutoUpdate: cfg.DisableAutoUpdate,
	}
	if s.Operator == "" {
		s.Operator = OperatorCMCC
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	enc, err := EncryptData(raw)
	if err != nil {
		return err
	}
	return atomicWriteFile(ConfigPath(), enc)
}

func LoadEncryptedConfig() (Config, error) {
	enc, err := os.ReadFile(ConfigPath())
	if err != nil {
		return Config{}, err
	}
	raw, err := DecryptData(enc)
	if err != nil {
		return Config{}, err
	}
	var s storedConfig
	if err := json.Unmarshal(raw, &s); err != nil {
		return Config{}, err
	}
	mode := s.ActiveMode
	if mode == "" {
		mode = ModeSelf
	}
	if mode == ModeSelf && (s.Account == "" || s.Password == "") {
		return Config{}, errors.New("账号或密码为空")
	}
	op := s.Operator
	if op == "" {
		op = OperatorCMCC
	}
	hubURL := s.CloudHubURL
	if hubURL == "" {
		hubURL = "https://keeper.egami.online"
	}
	adminToken := s.OwnerAdminToken
	if adminToken == "" {
		adminToken = generateAdminToken()
	}
	return Config{
		ActiveMode:      mode,
		Account:         s.Account,
		Password:        s.Password,
		Operator:        op,
		PortalURL:       s.PortalURL,
		CampusMode:      campus.Auto,
		OwnerAdminToken: adminToken,
		CloudHubURL:     hubURL,
		FriendShareCode: s.FriendShareCode,
		FriendShares:    s.FriendShares,
		PublishedShares:   s.PublishedShares,
		DisableAutoUpdate: s.DisableAutoUpdate,
	}, nil
}
