package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

type config struct {
	account        string
	password       string
	portalURL      string
	campusMode     campus
	accountSuffix  string
	suffixExplicit bool
}

func loadConfig(path string) (config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return config{}, fmt.Errorf("读取登录配置失败：%w", err)
	}
	vars := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "\ufeff"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		vars[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(value), `"'`)
	}
	id, password := vars["STUDENT_ID"], vars["PASSWORD"]
	if id == "" || password == "" {
		return config{}, errors.New(".env 必须包含 STUDENT_ID 和 PASSWORD")
	}
	_, explicit := vars["ACCOUNT_SUFFIX"]
	suffix := vars["ACCOUNT_SUFFIX"]
	if suffix != "" && !strings.HasPrefix(suffix, "@") {
		return config{}, errors.New("ACCOUNT_SUFFIX 必须以 @ 开头，或留空")
	}
	mode := campus(strings.ToLower(vars["CAMPUS"]))
	if mode == "" {
		mode = campusAuto
	}
	if mode != campusAuto && mode != campusWenchang && mode != campusNanhu {
		return config{}, errors.New("CAMPUS 只能为 auto、wenchang 或 nanhu")
	}
	return config{account: id, password: password, portalURL: vars["PORTAL_URL"], campusMode: mode, accountSuffix: suffix, suffixExplicit: explicit}, nil
}

func (c config) accountFor(site campus) string {
	if strings.Contains(c.account, "@") {
		return c.account
	}
	if c.suffixExplicit {
		return c.account + c.accountSuffix
	}
	switch site {
	case campusWenchang:
		return c.account + "@xyw"
	case campusNanhu:
		return c.account + "@cmcc"
	default:
		return c.account
	}
}

func mask(s string) string {
	if len(s) <= 4 {
		return "****"
	}
	return s[:2] + "****" + s[len(s)-2:]
}
