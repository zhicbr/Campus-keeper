package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

type operator string

const (
	operatorCMCC    operator = "cmcc"
	operatorTelecom operator = "telecom"
	operatorUnicom  operator = "unicom"
	operatorCampus  operator = "campus"
)

func (op operator) displayName() string {
	switch op {
	case operatorCMCC:
		return "移动"
	case operatorTelecom:
		return "电信"
	case operatorUnicom:
		return "联通"
	case operatorCampus:
		return "校园网"
	default:
		return "移动"
	}
}

func (op operator) suffix() string {
	switch op {
	case operatorCMCC:
		return "@cmcc"
	case operatorTelecom:
		return "@dx"
	case operatorUnicom:
		return "@lt"
	case operatorCampus:
		return "@xyw"
	default:
		return "@cmcc"
	}
}

type config struct {
	account        string
	password       string
	operator       operator
	portalURL      string
	campusMode     campus
	accountSuffix  string
	suffixExplicit bool
}

type storedConfig struct {
	Account   string   `json:"account"`
	Password  string   `json:"password"`
	Operator  operator `json:"operator"`
	PortalURL string   `json:"portal_url,omitempty"`
}

func (c config) valid() bool {
	return strings.TrimSpace(c.account) != "" && strings.TrimSpace(c.password) != ""
}

func (c config) operatorName() string {
	if c.operator != "" {
		return c.operator.displayName()
	}
	return "移动"
}

func (c config) accountFor(site campus) string {
	acc := strings.TrimSpace(c.account)
	if strings.Contains(acc, "@") {
		return acc
	}
	if c.suffixExplicit {
		return acc + c.accountSuffix
	}
	if c.operator != "" {
		return acc + c.operator.suffix()
	}
	return acc + "@xyw"
}

func mask(s string) string {
	if len(s) <= 4 {
		return "****"
	}
	return s[:2] + "****" + s[len(s)-2:]
}

func appDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	dir := filepath.Join(home, ".campus-keeper")
	_ = os.MkdirAll(dir, 0700)
	return dir
}

func configPath() string {
	return filepath.Join(appDataDir(), "config.dat")
}

func logPath() string {
	return filepath.Join(appDataDir(), "keeper.log")
}

func saveEncryptedConfig(cfg config) error {
	s := storedConfig{
		Account:   strings.TrimSpace(cfg.account),
		Password:  cfg.password,
		Operator:  cfg.operator,
		PortalURL: cfg.portalURL,
	}
	if s.Operator == "" {
		s.Operator = operatorCMCC
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	enc, err := encryptData(raw)
	if err != nil {
		return err
	}
	return os.WriteFile(configPath(), enc, 0600)
}

func loadEncryptedConfig() (config, error) {
	enc, err := os.ReadFile(configPath())
	if err != nil {
		return config{}, err
	}
	raw, err := decryptData(enc)
	if err != nil {
		return config{}, err
	}
	var s storedConfig
	if err := json.Unmarshal(raw, &s); err != nil {
		return config{}, err
	}
	if s.Account == "" || s.Password == "" {
		return config{}, errors.New("账号或密码为空")
	}
	op := s.Operator
	if op == "" {
		op = operatorCMCC
	}
	return config{
		account:    s.Account,
		password:   s.Password,
		operator:   op,
		portalURL:  s.PortalURL,
		campusMode: campusAuto,
	}, nil
}
