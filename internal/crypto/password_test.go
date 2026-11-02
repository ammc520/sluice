package crypto

import "testing"

func TestPasswordRoundTrip(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword(hash, "correct horse battery staple") {
		t.Fatal("correct password rejected")
	}
	if VerifyPassword(hash, "wrong") {
		t.Fatal("wrong password accepted")
	}
	if VerifyPassword("not-a-hash", "x") {
		t.Fatal("malformed hash accepted")
	}
}

func TestPasswordParams(t *testing.T) {
	hash, _ := HashPassword("x")
	m, tm, p, err := ParsePasswordParams(hash)
	if err != nil || m != 65536 || tm != 1 || p != 4 {
		t.Fatalf("m=%d t=%d p=%d err=%v", m, tm, p, err)
	}
}
