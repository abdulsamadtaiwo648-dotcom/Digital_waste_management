package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
)

func (s *server) forgotPassword(w http.ResponseWriter, r *http.Request) {
	if !s.devResetLinks {
		writeError(w, http.StatusServiceUnavailable, "recovery_unavailable", "Account recovery is not configured. Contact the service administrator.")
		return
	}
	var in struct {
		Email string `json:"email"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	if !validEmail(in.Email) {
		writeError(w, 422, "invalid_email", "Enter a valid email address")
		return
	}
	// The token is returned only in an explicitly enabled non-production environment.
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		writeError(w, 500, "recovery_unavailable", "A recovery request could not be created")
		return
	}
	token := base64.RawURLEncoding.EncodeToString(random)
	digest := sha256.Sum256([]byte(token))
	tokenHash := hex.EncodeToString(digest[:])
	var userID int64
	// The public recovery endpoint is resident-only. Staff recovery must use a
	// separately protected internal flow so staff accounts cannot be targeted
	// through the public site.
	err := s.db.QueryRow(r.Context(), `SELECT id FROM users WHERE email=$1 AND role='citizen' AND deleted_at IS NULL`, in.Email).Scan(&userID)
	if err == nil {
		tx, beginErr := s.db.Begin(r.Context())
		if beginErr != nil {
			writeError(w, 500, "recovery_unavailable", "A recovery request could not be created")
			return
		}
		defer tx.Rollback(r.Context())
		_, err = tx.Exec(r.Context(), `UPDATE password_reset_tokens SET consumed_at=now() WHERE user_id=$1 AND consumed_at IS NULL`, userID)
		if err == nil {
			_, err = tx.Exec(r.Context(), `INSERT INTO password_reset_tokens(user_id,token_hash,expires_at) VALUES($1,$2,now()+interval '30 minutes')`, userID, tokenHash)
		}
		if err == nil {
			err = tx.Commit(r.Context())
		}
		if err != nil {
			writeError(w, 500, "recovery_unavailable", "A recovery request could not be created")
			return
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 500, "recovery_unavailable", "A recovery request could not be created")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"message": "If the account exists, recovery instructions can be used within 30 minutes.", "development_token": token})
}

func (s *server) resetPassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if len(in.Token) < 32 || len(in.Token) > 128 || len(in.Password) < 12 {
		writeError(w, 422, "invalid_reset", "Use a valid recovery token and a password of at least 12 characters")
		return
	}
	digest := sha256.Sum256([]byte(in.Token))
	tokenHash := hex.EncodeToString(digest[:])
	passwordHash, err := hashPassword(in.Password)
	if err != nil {
		writeError(w, 500, "password_reset_failed", "Password could not be changed")
		return
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "password_reset_failed", "Password could not be changed")
		return
	}
	defer tx.Rollback(r.Context())
	var userID int64
	err = tx.QueryRow(r.Context(), `UPDATE password_reset_tokens t SET consumed_at=now() FROM users u WHERE t.user_id=u.id AND u.role='citizen' AND u.deleted_at IS NULL AND t.token_hash=$1 AND t.consumed_at IS NULL AND t.expires_at>now() RETURNING t.user_id`, tokenHash).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 400, "invalid_reset", "Recovery token is invalid or expired")
		return
	}
	if err != nil {
		writeError(w, 500, "password_reset_failed", "Password could not be changed")
		return
	}
	tag, err := tx.Exec(r.Context(), `UPDATE users SET password_hash=$1,session_version=session_version+1,updated_at=now() WHERE id=$2 AND deleted_at IS NULL`, passwordHash, userID)
	if err != nil {
		writeError(w, 500, "password_reset_failed", "Password could not be changed")
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, 400, "invalid_reset", "Recovery token is invalid or expired")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "password_reset_failed", "Password could not be changed")
		return
	}
	writeJSON(w, 200, map[string]string{"message": "Password changed. Sign in with your new password."})
}
