package sharing

import (
	"testing"
)

func TestShareCodeRoundTrip(t *testing.T) {
	key, err := GenerateRandomKey()
	if err != nil {
		t.Fatalf("GenerateRandomKey error: %v", err)
	}

	shareID, err := GenerateShareID()
	if err != nil {
		t.Fatalf("GenerateShareID error: %v", err)
	}

	code := FormatShareCode(shareID, key)
	parsedID, parsedKey, err := ParseShareCode(code)
	if err != nil {
		t.Fatalf("ParseShareCode error: %v", err)
	}

	if parsedID != shareID {
		t.Errorf("got shareID %s, want %s", parsedID, shareID)
	}
	if string(parsedKey) != string(key) {
		t.Errorf("key mismatch")
	}
}

func TestEncryptDecryptCredentials(t *testing.T) {
	key, err := GenerateRandomKey()
	if err != nil {
		t.Fatalf("GenerateRandomKey error: %v", err)
	}

	creds := SharedCredentials{
		Account:  "12345678",
		Password: "SecretPassword123!",
		Operator: "cmcc",
		Memo:     "测试移动共享",
	}

	encrypted, err := EncryptCredentials(creds, key)
	if err != nil {
		t.Fatalf("EncryptCredentials error: %v", err)
	}

	decrypted, err := DecryptCredentials(encrypted, key)
	if err != nil {
		t.Fatalf("DecryptCredentials error: %v", err)
	}

	if decrypted.Account != creds.Account || decrypted.Password != creds.Password || decrypted.Operator != creds.Operator || decrypted.Memo != creds.Memo {
		t.Errorf("Decrypted creds %+v mismatch with original %+v", decrypted, creds)
	}

	// Wrong key should fail
	wrongKey, _ := GenerateRandomKey()
	_, err = DecryptCredentials(encrypted, wrongKey)
	if err == nil {
		t.Errorf("Expected decryption error with wrong key, got nil")
	}
}
