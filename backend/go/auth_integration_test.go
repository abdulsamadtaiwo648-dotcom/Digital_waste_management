package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLogoutRevocationErrorsAreNotReportedAsSuccess(t *testing.T) {
	pool, cleanup := managedAreaTestDatabase(t, testingDatabaseURL(t))
	defer cleanup()
	ctx := context.Background()
	s := &server{db: pool, sessionSecret: []byte("test-session-secret-that-is-long-enough-32")}

	var successUserID int64
	if err := pool.QueryRow(ctx, `INSERT INTO users(full_name,email,phone,password_hash,role) VALUES('Logout Success','logout-success@example.test','+234000000004','test-hash','citizen') RETURNING id`).Scan(&successUserID); err != nil {
		t.Fatal(err)
	}
	response := logoutWithSignedCookie(t, s, successUserID, 0)
	if response.Code != http.StatusNoContent || !cookieCleared(response, residentSessionCookie) {
		t.Fatalf("successful logout returned %d or failed to clear cookie", response.Code)
	}
	var version int64
	if err := pool.QueryRow(ctx, `SELECT session_version FROM users WHERE id=$1`, successUserID).Scan(&version); err != nil || version != 1 {
		t.Fatalf("successful logout did not revoke session: version=%d err=%v", version, err)
	}

	_, err := pool.Exec(ctx, `CREATE FUNCTION reject_session_revocation() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.session_version > OLD.session_version THEN RAISE EXCEPTION 'session revocation intentionally rejected'; END IF;
			RETURN NEW;
		END $$`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `CREATE TRIGGER reject_session_revocation BEFORE UPDATE ON users FOR EACH ROW EXECUTE FUNCTION reject_session_revocation()`); err != nil {
		t.Fatal(err)
	}
	var failureUserID int64
	if err := pool.QueryRow(ctx, `INSERT INTO users(full_name,email,phone,password_hash,role) VALUES('Logout Failure','logout-failure@example.test','+234000000005','test-hash','citizen') RETURNING id`).Scan(&failureUserID); err != nil {
		t.Fatal(err)
	}
	response = logoutWithSignedCookie(t, s, failureUserID, 0)
	if response.Code != http.StatusInternalServerError || !cookieCleared(response, residentSessionCookie) {
		t.Fatalf("failed revocation returned %d or failed to clear browser cookie: %s", response.Code, response.Body.String())
	}
	if err := pool.QueryRow(ctx, `SELECT session_version FROM users WHERE id=$1`, failureUserID).Scan(&version); err != nil || version != 0 {
		t.Fatalf("failed revocation unexpectedly changed session version: version=%d err=%v", version, err)
	}
}

func TestSessionMiddlewareDistinguishesDatabaseFailure(t *testing.T) {
	pool, cleanup := managedAreaTestDatabase(t, testingDatabaseURL(t))
	defer cleanup()
	s := &server{db: pool, sessionSecret: []byte("test-session-secret-that-is-long-enough-32")}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })

	response := sessionRequest(t, s, next)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("validly signed token for absent account returned %d, want 401", response.Code)
	}
	if _, err := pool.Exec(context.Background(), `ALTER TABLE users RENAME TO users_unavailable_for_test`); err != nil {
		t.Fatal(err)
	}
	response = sessionRequest(t, s, next)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("session lookup database failure returned %d, want 500: %s", response.Code, response.Body.String())
	}
}

func TestLoginDistinguishesDatabaseFailureFromBadCredentials(t *testing.T) {
	pool, cleanup := managedAreaTestDatabase(t, testingDatabaseURL(t))
	defer cleanup()
	s := &server{db: pool}
	body := `{"email":"missing@example.test","password":"not-a-valid-password"}`
	response := callAs(s.login, http.MethodPost, "/api/v1/auth/login", body, user{})
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unknown account login returned %d, want 401", response.Code)
	}
	if _, err := pool.Exec(context.Background(), `ALTER TABLE users RENAME TO users_unavailable_for_test`); err != nil {
		t.Fatal(err)
	}
	response = callAs(s.login, http.MethodPost, "/api/v1/auth/login", body, user{})
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("login database failure returned %d, want 500: %s", response.Code, response.Body.String())
	}
}

func sessionRequest(t *testing.T, s *server, next http.Handler) *httptest.ResponseRecorder {
	t.Helper()
	token, err := s.signSession(sessionClaims{UserID: 999999, Version: 0, Expiry: time.Now().Add(time.Hour).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	request.AddCookie(&http.Cookie{Name: residentSessionCookie, Value: token})
	response := httptest.NewRecorder()
	s.requireSession(residentSessionCookie, func(user) bool { return true }, next).ServeHTTP(response, request)
	return response
}

func logoutWithSignedCookie(t *testing.T, s *server, userID, version int64) *httptest.ResponseRecorder {
	t.Helper()
	token, err := s.signSession(sessionClaims{UserID: userID, Version: version, Expiry: time.Now().Add(time.Hour).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	request.AddCookie(&http.Cookie{Name: residentSessionCookie, Value: token})
	response := httptest.NewRecorder()
	s.logout(response, request)
	return response
}

func cookieCleared(response *httptest.ResponseRecorder, name string) bool {
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == name && cookie.MaxAge < 0 && cookie.Value == "" {
			return true
		}
	}
	return false
}
