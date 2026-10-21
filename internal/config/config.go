package config

import (
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
	Account        string        `json:"account"`
	Password       string        `json:"password"`
	Operator       Operator      `json:"operator"`
	PortalURL      string        `json:"portal_url,omitempty"`
	CampusMode     campus.Campus `json:"campus_mode,omitempty"`
	AccountSuffix  string        `json:"account_suffix,omitempty"`
	SuffixExplicit bool          `json:"suffix_explicit,omitempty"`
}

type storedConfig struct {
	Account   string   `json:"account"`
	Password  string   `json:"password"`
	Operator  Operator `json:"operator"`
	PortalURL string   `json:"portal_url,omitempty"`
}

func (c Config) Valid() bool {
	return strings.TrimSpace(c.Account) != "" && strings.TrimSpace(c.Password) != ""
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

func SaveEncryptedConfig(cfg Config) error {
	s := storedConfig{
		Account:   strings.TrimSpace(cfg.Account),
		Password:  cfg.Password,
		Operator:  cfg.Operator,
		PortalURL: cfg.PortalURL,
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
	return os.WriteFile(ConfigPath(), enc, 0600)
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
	if s.Account == "" || s.Password == "" {
		return Config{}, errors.New("账号或密码为空")
	}
	op := s.Operator
	if op == "" {
		op = OperatorCMCC
	}
	return Config{
		Account:    s.Account,
		Password:   s.Password,
		Operator:   op,
		PortalURL:  s.PortalURL,
		CampusMode: campus.Auto,
	}, nil
}
