//go:build windows

package config

import (
	"errors"
	"strings"
)

var (
	ErrEmptyAccount             = errors.New("账号不能为空")
	ErrAccountChangedNeedNewPwd = errors.New("更换账号时必须输入新账号的密码")
	ErrEmptyPassword            = errors.New("密码不能为空")
	ErrUnknownOperator          = errors.New("未知运营商选项")
)

// ConfigUpdateResult contains the updated config and whether any field actually changed.
type ConfigUpdateResult struct {
	NewConfig Config
	Changed   bool
}

// ParseOperator converts user-selected string to Operator enum, returning error for unknown values.
func ParseOperator(opStr string) (Operator, error) {
	switch strings.TrimSpace(opStr) {
	case "移动", "中国移动":
		return OperatorCMCC, nil
	case "电信", "中国电信":
		return OperatorTelecom, nil
	case "联通", "中国联通":
		return OperatorUnicom, nil
	case "校园网":
		return OperatorCampus, nil
	default:
		return "", ErrUnknownOperator
	}
}

// ResolveConfigUpdate determines if the submitted credentials/operator differ
// from current config. If nothing changed, Changed is false and no write/reconnect should happen.
//
// Rules:
// 1. Account cannot be empty.
// 2. Operator must be valid.
// 3. User input password has leading/trailing whitespace sanitized (TrimSpace) to prevent
//    accidental copy-paste whitespace.
// 4. Comparison for passwordChanged is strictly against current.Password (the actual raw stored password).
//    This ensures that historical passwords with accidental whitespace can be corrected by saving the trimmed version.
// 5. If account changed to a different user (!EqualFold), password cannot be empty.
// 6. If account is the same user (EqualFold), empty password reuses current password.
// 7. If account casing changed (e.g. TS123 vs ts123), it reuses current password,
//    updates newCfg.Account to the new casing, and marks Changed = true.
func ResolveConfigUpdate(account, inputPassword, opStr string, current Config) (ConfigUpdateResult, error) {
	account = strings.TrimSpace(account)
	if account == "" {
		return ConfigUpdateResult{}, ErrEmptyAccount
	}

	op, err := ParseOperator(opStr)
	if err != nil {
		return ConfigUpdateResult{}, err
	}

	curAccount := strings.TrimSpace(current.Account)
	isDifferentUser := curAccount != "" && !strings.EqualFold(account, curAccount)
	casingOrExactChanged := account != curAccount

	var finalPwd string
	var passwordChanged bool

	trimmedInputPwd := strings.TrimSpace(inputPassword)
	if trimmedInputPwd == "" {
		if isDifferentUser {
			return ConfigUpdateResult{}, ErrAccountChangedNeedNewPwd
		}
		if strings.TrimSpace(current.Password) == "" {
			return ConfigUpdateResult{}, ErrEmptyPassword
		}
		finalPwd = current.Password // Preserve current password as-is when user left field empty
		passwordChanged = false
	} else {
		finalPwd = trimmedInputPwd
		// Compare sanitized input against raw stored password so dirty whitespace can be fixed
		passwordChanged = finalPwd != current.Password
	}

	operatorChanged := op != current.Operator

	if !casingOrExactChanged && !passwordChanged && !operatorChanged {
		return ConfigUpdateResult{NewConfig: current, Changed: false}, nil
	}

	newCfg := current
	if casingOrExactChanged {
		newCfg.Account = account
	}
	if passwordChanged {
		newCfg.Password = finalPwd
	}
	if operatorChanged {
		newCfg.Operator = op
	}

	return ConfigUpdateResult{NewConfig: newCfg, Changed: true}, nil
}
