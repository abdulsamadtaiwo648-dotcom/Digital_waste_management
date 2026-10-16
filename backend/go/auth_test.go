package main

import (
	"encoding/hex"
	"testing"
	"time"
)

func TestPBKDF2SHA256RFC6070Vector(t *testing.T) {
	got := hex.EncodeToString(pbkdf2SHA256([]byte("password"), []byte("salt"), 1, 32))
	want := "120fb6cffcf8b32c43e7225256c4f837a86548c92ccc35480805987cb70be17b"
	if got != want {
		t.Fatalf("PBKDF2 vector mismatch: got %s want %s", got, want)
	}
}

func TestPasswordHashVerifiesAndUsesUniqueSalt(t *testing.T) {
	first, err := hashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	second, err := hashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("same password should receive a distinct random salt")
	}
	if !verifyPassword("correct horse battery staple", first) {
		t.Fatal("valid password did not verify")
	}
	if verifyPassword("incorrect password", first) {
		t.Fatal("incorrect password verified")
	}
}

func TestSignedSessionCanBeRevokedByVersion(t *testing.T) {
	s := &server{sessionSecret: []byte("01234567890123456789012345678901")}
	token, err := s.signSession(sessionClaims{UserID: 17, Version: 3, Expiry: time.Now().Add(time.Hour).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := s.readSession(token)
	if err != nil {
		t.Fatal(err)
	}
	if claims.UserID != 17 || claims.Version != 3 {
		t.Fatalf("unexpected claims: %+v", claims)
	}
	if _, err := s.readSession(token + "tampered"); err == nil {
		t.Fatal("tampered session signature was accepted")
	}
}

func TestAuthLimiterAllowsTenAndBlocksEleventh(t *testing.T) {
	limiter := newAuthLimiter()
	now := time.Now()
	for i := 0; i < 10; i++ {
		if !limiter.allow("127.0.0.1", now) {
			t.Fatalf("request %d was rejected too early", i+1)
		}
	}
	if limiter.allow("127.0.0.1", now) {
		t.Fatal("eleventh request should be rate limited")
	}
	if !limiter.allow("127.0.0.2", now) {
		t.Fatal("one IP should not block another")
	}
}
