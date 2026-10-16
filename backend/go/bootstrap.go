package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func bootstrapAdmin(args []string) error {
	if len(args) != 3 {
		return errors.New("usage: go run ./backend/go bootstrap-admin <full-name> <email> <phone>")
	}
	name := strings.TrimSpace(args[0])
	email := strings.ToLower(strings.TrimSpace(args[1]))
	phone := strings.TrimSpace(args[2])
	password := os.Getenv("BOOTSTRAP_ADMIN_PASSWORD")
	if len(name) < 2 || !validEmail(email) || len(phone) < 7 || len(password) < 14 {
		return errors.New("provide valid account details and BOOTSTRAP_ADMIN_PASSWORD with at least 14 characters")
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return errors.New("DATABASE_URL must be set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	db, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(72104321)`); err != nil {
		return err
	}
	var admins int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM users WHERE role='system_administrator' AND deleted_at IS NULL`).Scan(&admins); err != nil {
		return err
	}
	if admins > 0 {
		return errors.New("an active system administrator already exists; bootstrap is first-use only")
	}
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}
	var id int64
	if err = tx.QueryRow(ctx, `INSERT INTO users(full_name,email,phone,password_hash,role,terms_accepted_at)
		VALUES($1,$2,$3,$4,'system_administrator',now()) RETURNING id`, name, email, phone, hash).Scan(&id); err != nil {
		return fmt.Errorf("create administrator: %w", err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_logs(actor_id,action,resource_type,resource_id) VALUES($1,'first_admin_bootstrapped','user',$2)`, id, fmt.Sprint(id)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
