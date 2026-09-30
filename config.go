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
	if !strings.Contains(id, "@") {
		id += vars["ACCOUNT_SUFFIX"]
	}
	return config{account: id, password: password, portalURL: vars["PORTAL_URL"], suffixExplicit: explicit}, nil
}

func mask(s string) string {
	if len(s) <= 4 {
		return "****"
	}
	return s[:2] + "****" + s[len(s)-2:]
}
