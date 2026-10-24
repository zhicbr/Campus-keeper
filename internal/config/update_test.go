//go:build windows

package config

import (
	"errors"
	"testing"
)

func TestParseOperator(t *testing.T) {
	valid := map[string]Operator{
		"移动":    OperatorCMCC,
		"中国移动":  OperatorCMCC,
		"电信":    OperatorTelecom,
		"中国电信":  OperatorTelecom,
		"联通":    OperatorUnicom,
		"中国联通":  OperatorUnicom,
		"校园网":   OperatorCampus,
		" 移动 ":  OperatorCMCC,
	}

	for input, expected := range valid {
		got, err := ParseOperator(input)
		if err != nil {
			t.Fatalf("ParseOperator(%q) unexpected error: %v", input, err)
		}
		if got != expected {
			t.Fatalf("ParseOperator(%q) = %v, want %v", input, got, expected)
		}
	}

	invalid := []string{"", "未知", "广电", "other", "   "}
	for _, input := range invalid {
		got, err := ParseOperator(input)
		if !errors.Is(err, ErrUnknownOperator) {
			t.Fatalf("ParseOperator(%q) expected ErrUnknownOperator, got err=%v, res=%v", input, err, got)
		}
	}
}

func TestResolveConfigUpdate(t *testing.T) {
	baseConfig := Config{
		Account:  "student01",
		Password: "OldSecretPassword123!",
		Operator: OperatorCMCC,
	}

	tests := []struct {
		name          string
		account       string
		inputPassword string
		opStr         string
		currConfig    Config
		wantChanged   bool
		wantAccount   string
		wantPassword  string
		wantOperator  Operator
		wantErr       error
	}{
		{
			name:          "Empty account returns ErrEmptyAccount and zero value",
			account:       "   ",
			inputPassword: "any",
			opStr:         "移动",
			currConfig:    baseConfig,
			wantErr:       ErrEmptyAccount,
		},
		{
			name:          "Unknown operator returns ErrUnknownOperator and zero value",
			account:       "student01",
			inputPassword: "",
			opStr:         "中国广电",
			currConfig:    baseConfig,
			wantErr:       ErrUnknownOperator,
		},
		{
			name:          "Identical values return Changed = false (no-op)",
			account:       "student01",
			inputPassword: "",
			opStr:         "移动",
			currConfig:    baseConfig,
			wantChanged:   false,
			wantAccount:   "student01",
			wantPassword:  "OldSecretPassword123!",
			wantOperator:  OperatorCMCC,
		},
		{
			name:          "Identical values with same plaintext password return Changed = false",
			account:       "student01",
			inputPassword: "OldSecretPassword123!",
			opStr:         "中国移动",
			currConfig:    baseConfig,
			wantChanged:   false,
			wantAccount:   "student01",
			wantPassword:  "OldSecretPassword123!",
			wantOperator:  OperatorCMCC,
		},
		{
			name:          "Same password with accidental trailing/leading whitespace is sanitized and returns Changed = false",
			account:       "student01",
			inputPassword: "  OldSecretPassword123!  ",
			opStr:         "中国移动",
			currConfig:    baseConfig,
			wantChanged:   false,
			wantAccount:   "student01",
			wantPassword:  "OldSecretPassword123!",
			wantOperator:  OperatorCMCC,
		},
		{
			name:          "New password with accidental trailing/leading whitespace is trimmed and stored clean",
			account:       "student01",
			inputPassword: "  BrandNewSecret456!  ",
			opStr:         "移动",
			currConfig:    baseConfig,
			wantChanged:   true,
			wantAccount:   "student01",
			wantPassword:  "BrandNewSecret456!",
			wantOperator:  OperatorCMCC,
		},
		{
			name:          "Existing password with whitespace can be corrected by saving clean password",
			account:       "student01",
			inputPassword: "old-secret",
			opStr:         "移动",
			currConfig: Config{
				Account:  "student01",
				Password: " old-secret ",
				Operator: OperatorCMCC,
			},
			wantChanged:  true,
			wantAccount:  "student01",
			wantPassword: "old-secret",
			wantOperator: OperatorCMCC,
		},
		{
			name:          "Casing change alone preserves password, updates casing and returns Changed = true",
			account:       "STUDENT01",
			inputPassword: "",
			opStr:         "移动",
			currConfig:    baseConfig,
			wantChanged:   true,
			wantAccount:   "STUDENT01",
			wantPassword:  "OldSecretPassword123!",
			wantOperator:  OperatorCMCC,
		},
		{
			name:          "Whitespace password '   ' treated as empty, preserving password if same user",
			account:       "student01",
			inputPassword: "   ",
			opStr:         "移动",
			currConfig:    baseConfig,
			wantChanged:   false,
			wantAccount:   "student01",
			wantPassword:  "OldSecretPassword123!",
			wantOperator:  OperatorCMCC,
		},
		{
			name:          "Only operator changed updates operator and returns Changed = true",
			account:       "student01",
			inputPassword: "",
			opStr:         "电信",
			currConfig:    baseConfig,
			wantChanged:   true,
			wantAccount:   "student01",
			wantPassword:  "OldSecretPassword123!",
			wantOperator:  OperatorTelecom,
		},
		{
			name:          "Only password changed updates password and returns Changed = true",
			account:       "student01",
			inputPassword: "BrandNewPassword789!",
			opStr:         "移动",
			currConfig:    baseConfig,
			wantChanged:   true,
			wantAccount:   "student01",
			wantPassword:  "BrandNewPassword789!",
			wantOperator:  OperatorCMCC,
		},
		{
			name:          "Account changed with empty password is rejected",
			account:       "student02",
			inputPassword: "",
			opStr:         "移动",
			currConfig:    baseConfig,
			wantErr:       ErrAccountChangedNeedNewPwd,
		},
		{
			name:          "Account changed with whitespace password is rejected",
			account:       "student02",
			inputPassword: "   ",
			opStr:         "移动",
			currConfig:    baseConfig,
			wantErr:       ErrAccountChangedNeedNewPwd,
		},
		{
			name:          "Account changed with new password updates both and returns Changed = true",
			account:       "student02",
			inputPassword: "NewUserPassword456!",
			opStr:         "联通",
			currConfig:    baseConfig,
			wantChanged:   true,
			wantAccount:   "student02",
			wantPassword:  "NewUserPassword456!",
			wantOperator:  OperatorUnicom,
		},
		{
			name:          "Initial setup with password returns Changed = true",
			account:       "student01",
			inputPassword: "InitialPassword!",
			opStr:         "校园网",
			currConfig:    Config{},
			wantChanged:   true,
			wantAccount:   "student01",
			wantPassword:  "InitialPassword!",
			wantOperator:  OperatorCampus,
		},
		{
			name:          "Initial setup with empty password is rejected",
			account:       "student01",
			inputPassword: "",
			opStr:         "校园网",
			currConfig:    Config{},
			wantErr:       ErrEmptyPassword,
		},
		{
			name:          "Current account non-empty but current password is empty or whitespace rejected",
			account:       "student01",
			inputPassword: "",
			opStr:         "校园网",
			currConfig: Config{
				Account:  "student01",
				Password: "   ",
			},
			wantErr: ErrEmptyPassword,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := ResolveConfigUpdate(tt.account, tt.inputPassword, tt.opStr, tt.currConfig)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("ResolveConfigUpdate() error = %v, wantErr = %v", err, tt.wantErr)
				}
				// Assert zero value on error
				if (res.NewConfig != Config{} || res.Changed) {
					t.Fatalf("ResolveConfigUpdate() expected zero ConfigUpdateResult on error, got: %+v", res)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveConfigUpdate() unexpected error: %v", err)
			}
			if res.Changed != tt.wantChanged {
				t.Fatalf("ResolveConfigUpdate() changed = %v, wantChanged = %v", res.Changed, tt.wantChanged)
			}
			if res.NewConfig.Account != tt.wantAccount {
				t.Fatalf("ResolveConfigUpdate() account = %q, want = %q", res.NewConfig.Account, tt.wantAccount)
			}
			if res.NewConfig.Password != tt.wantPassword {
				t.Fatalf("ResolveConfigUpdate() password = %q, want = %q", res.NewConfig.Password, tt.wantPassword)
			}
			if res.NewConfig.Operator != tt.wantOperator {
				t.Fatalf("ResolveConfigUpdate() operator = %v, want = %v", res.NewConfig.Operator, tt.wantOperator)
			}
		})
	}
}
