package main

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const residentSessionCookie = "dwmp_resident_session"
const passwordIterations = 310000

type user struct {
	ID    int64  `json:"id"`
	Name  string `json:"full_name"`
	Email string `json:"email"`
	Phone string `json:"phone"`
	Role  string `json:"role"`
}

type sessionClaims struct {
	UserID  int64 `json:"sub"`
	Version int64 `json:"ver"`
	Expiry  int64 `json:"exp"`
}

type contextKey string

const userContextKey contextKey = "authenticated_user"

func (s *server) register(w http.ResponseWriter, r *http.Request) {
	var input struct {
		FullName      string `json:"full_name"`
		Email         string `json:"email"`
		Phone         string `json:"phone"`
		Password      string `json:"password"`
		TermsAccepted bool   `json:"terms_accepted"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.FullName = strings.TrimSpace(input.FullName)
	input.Email = strings.ToLower(strings.TrimSpace(input.Email))
	input.Phone = strings.TrimSpace(input.Phone)
	if len(input.FullName) < 2 || len(input.FullName) > 120 || len(input.Phone) < 7 || len(input.Phone) > 32 || !validEmail(input.Email) || len(input.Password) < 12 || !input.TermsAccepted {
		writeError(w, http.StatusUnprocessableEntity, "invalid_registration", "Enter a valid name, email, phone number, a password of at least 12 characters, and accept the terms")
		return
	}
	hash, err := hashPassword(input.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "password_setup_failed", "Account could not be created")
		return
	}
	var created user
	var sessionVersion int64
	err = s.db.QueryRow(r.Context(), `INSERT INTO users (full_name,email,phone,password_hash,role,terms_accepted_at)
		VALUES ($1,$2,$3,$4,'citizen',now()) RETURNING id,full_name,email,phone,role,session_version`, input.FullName, input.Email, input.Phone, hash).
		Scan(&created.ID, &created.Name, &created.Email, &created.Phone, &created.Role, &sessionVersion)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			writeError(w, http.StatusConflict, "email_in_use", "An account with this email already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "account_creation_failed", "Account could not be created")
		return
	}
	s.setSession(w, created.ID, sessionVersion, residentSessionCookie)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{"user": created})
}

func (s *server) login(w http.ResponseWriter, r *http.Request) {
	s.loginForPortal(w, r, false)
}

func (s *server) staffLogin(w http.ResponseWriter, r *http.Request) {
	s.loginForPortal(w, r, true)
}

func (s *server) loginForPortal(w http.ResponseWriter, r *http.Request, staff bool) {
	var input struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	var found user
	var encoded string
	var sessionVersion int64
	err := s.db.QueryRow(r.Context(), `SELECT id,full_name,email,phone,role,password_hash,session_version FROM users WHERE email=$1 AND deleted_at IS NULL`, strings.ToLower(strings.TrimSpace(input.Email))).
		Scan(&found.ID, &found.Name, &found.Email, &found.Phone, &found.Role, &encoded, &sessionVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "Email or password is incorrect")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "authentication_unavailable", "Sign in could not be completed. Please try again.")
		return
	}
	wrongWorkspace := staff && ((s.serviceRole == "management" && found.Role != "operations_manager") || (s.serviceRole == "admin" && found.Role != "system_administrator"))
	if !verifyPassword(input.Password, encoded) || (staff && found.Role == "citizen") || (!staff && found.Role != "citizen") || wrongWorkspace {
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "Email or password is incorrect")
		return
	}
	cookie := residentSessionCookie
	if staff {
		cookie = s.staffCookie
	}
	s.setSession(w, found.ID, sessionVersion, cookie)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"user": found})
}

func (s *server) logout(w http.ResponseWriter, r *http.Request) {
	s.logoutPortal(w, r, residentSessionCookie)
}

func (s *server) staffLogout(w http.ResponseWriter, r *http.Request) {
	s.logoutPortal(w, r, s.staffCookie)
}

func (s *server) logoutPortal(w http.ResponseWriter, r *http.Request, cookieName string) {
	var revokeErr error
	if cookie, err := r.Cookie(cookieName); err == nil {
		if claims, err := s.readSession(cookie.Value); err == nil {
			_, revokeErr = s.db.Exec(r.Context(), `UPDATE users SET session_version=session_version+1 WHERE id=$1`, claims.UserID)
		}
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", HttpOnly: true, Secure: s.cookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: -1, Expires: time.Unix(0, 0)})
	if revokeErr != nil {
		writeError(w, http.StatusInternalServerError, "logout_failed", "Your session could not be revoked. Please try again.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) me(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"user": r.Context().Value(userContextKey).(user)})
}

func (s *server) staffMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"user": r.Context().Value(userContextKey).(user)})
}

func (s *server) updateProfile(w http.ResponseWriter, r *http.Request) {
	var input struct {
		FullName string `json:"full_name"`
		Phone    string `json:"phone"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.FullName = strings.TrimSpace(input.FullName)
	input.Phone = strings.TrimSpace(input.Phone)
	if len(input.FullName) < 2 || len(input.FullName) > 120 || len(input.Phone) < 7 || len(input.Phone) > 32 {
		writeError(w, 422, "invalid_profile", "Enter a valid name and phone number")
		return
	}
	current := r.Context().Value(userContextKey).(user)
	var updated user
	err := s.db.QueryRow(r.Context(), `UPDATE users SET full_name=$1,phone=$2,updated_at=now() WHERE id=$3 AND deleted_at IS NULL RETURNING id,full_name,email,phone,role`, input.FullName, input.Phone, current.ID).Scan(&updated.ID, &updated.Name, &updated.Email, &updated.Phone, &updated.Role)
	if err != nil {
		writeError(w, 500, "profile_update_failed", "Profile could not be updated")
		return
	}
	writeJSON(w, 200, map[string]any{"user": updated})
}

func (s *server) requireUser(next http.Handler) http.Handler {
	return s.requireSession(residentSessionCookie, func(current user) bool { return current.Role == "citizen" }, next)
}

func (s *server) requireStaff(next http.Handler) http.Handler {
	return s.requireSession(s.staffCookie, func(current user) bool { return current.Role != "citizen" }, next)
}

func (s *server) requireSession(cookieName string, allowed func(user) bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(cookieName)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "authentication_required", "Sign in to continue")
			return
		}
		claims, err := s.readSession(cookie.Value)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "invalid_session", "Please sign in again")
			return
		}
		var current user
		var sessionVersion int64
		err = s.db.QueryRow(r.Context(), `SELECT id,full_name,email,phone,role,session_version FROM users WHERE id=$1 AND deleted_at IS NULL`, claims.UserID).Scan(&current.ID, &current.Name, &current.Email, &current.Phone, &current.Role, &sessionVersion)
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusUnauthorized, "account_unavailable", "Please sign in again")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "session_verification_unavailable", "Your session could not be verified. Please try again.")
			return
		}
		if sessionVersion != claims.Version {
			writeError(w, http.StatusUnauthorized, "session_revoked", "Please sign in again")
			return
		}
		if !allowed(current) {
			writeError(w, http.StatusForbidden, "insufficient_permissions", "This account cannot access this portal")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userContextKey, current)))
	})
}

func (s *server) requireRole(role string, next http.Handler) http.Handler {
	return s.requireSession(s.staffCookie, func(current user) bool { return current.Role == role || current.Role == "system_administrator" }, next)
}

func (s *server) setSession(w http.ResponseWriter, userID, version int64, cookieName string) {
	token, _ := s.signSession(sessionClaims{UserID: userID, Version: version, Expiry: time.Now().Add(12 * time.Hour).Unix()})
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: token, Path: "/", HttpOnly: true, Secure: s.cookieSecure, SameSite: http.SameSiteLaxMode, Expires: time.Now().Add(12 * time.Hour), MaxAge: 12 * 60 * 60})
}

func (s *server) signSession(claims sessionClaims) (string, error) {
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, s.sessionSecret)
	_, _ = mac.Write([]byte(encoded))
	return encoded + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func (s *server) readSession(token string) (sessionClaims, error) {
	var claims sessionClaims
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return claims, errors.New("malformed session")
	}
	provided, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return claims, errors.New("malformed signature")
	}
	mac := hmac.New(sha256.New, s.sessionSecret)
	_, _ = mac.Write([]byte(parts[0]))
	if subtle.ConstantTimeCompare(provided, mac.Sum(nil)) != 1 {
		return claims, errors.New("invalid signature")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return claims, err
	}
	if err = json.Unmarshal(payload, &claims); err != nil || claims.UserID <= 0 || claims.Expiry <= time.Now().Unix() {
		return claims, errors.New("expired session")
	}
	return claims, nil
}

func hashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	derived := pbkdf2SHA256([]byte(password), salt, passwordIterations, 32)
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s", passwordIterations, base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(derived)), nil
}

func verifyPassword(password, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
		return false
	}
	iterations, err := strconv.Atoi(parts[1])
	if err != nil || iterations < 100000 || iterations > 1000000 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil || len(salt) < 16 {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil || len(want) != 32 {
		return false
	}
	got := pbkdf2SHA256([]byte(password), salt, iterations, len(want))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// PBKDF2 is implemented with the standard HMAC primitive so passwords are never stored directly.
func pbkdf2SHA256(password, salt []byte, iterations, keyLen int) []byte {
	const hLen = 32
	blocks := (keyLen + hLen - 1) / hLen
	derived := make([]byte, 0, blocks*hLen)
	for block := 1; block <= blocks; block++ {
		mac := hmac.New(sha256.New, password)
		_, _ = mac.Write(salt)
		_, _ = mac.Write([]byte{byte(block >> 24), byte(block >> 16), byte(block >> 8), byte(block)})
		u := mac.Sum(nil)
		t := append([]byte(nil), u...)
		for i := 1; i < iterations; i++ {
			mac = hmac.New(sha256.New, password)
			_, _ = mac.Write(u)
			u = mac.Sum(nil)
			for j := range t {
				t[j] ^= u[j]
			}
		}
		derived = append(derived, t...)
	}
	return derived[:keyLen]
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "Request body is not valid")
		return false
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid_json", "Request body must contain one JSON value")
		return false
	}
	return true
}

func validEmail(value string) bool {
	addr, err := mail.ParseAddress(value)
	return err == nil && addr.Address == value && len(value) <= 254
}

func (s *server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && origin != s.serviceOrigin {
			writeError(w, http.StatusForbidden, "origin_not_allowed", "Request origin is not allowed")
			return
		}
		if origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Add("Vary", "Origin")
		}
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Device-ID, X-Device-Token, Idempotency-Key")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
