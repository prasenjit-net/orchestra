package auth

import "testing"

func TestPasswordHashAndVerification(t *testing.T) {
	password := "Secur3-password-alpha!"
	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	if hash == password || hash == "" {
		t.Fatal("HashPassword() did not return an encoded hash")
	}
	valid, needsRehash := VerifyPassword(hash, password)
	if !valid || needsRehash {
		t.Fatalf("VerifyPassword() = (%v, %v), want (true, false)", valid, needsRehash)
	}
	if valid, _ := VerifyPassword(hash, "incorrect-password!"); valid {
		t.Fatal("VerifyPassword() accepted an incorrect password")
	}
}

func TestPasswordPolicyRejectsWeakValues(t *testing.T) {
	for _, password := range []string{"short", "password12345", "                "} {
		if _, err := HashPassword(password); err == nil {
			t.Fatalf("HashPassword(%q) unexpectedly succeeded", password)
		}
	}
}
