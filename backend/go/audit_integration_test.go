package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPrivilegedAuditTransactions(t *testing.T) {
	dsn := testingDatabaseURL(t)
	pool, cleanup := managedAreaTestDatabase(t, dsn)
	defer cleanup()
	s := &server{db: pool}
	ctx := context.Background()
	var adminID int64
	if err := pool.QueryRow(ctx, `INSERT INTO users(full_name,email,phone,password_hash,role) VALUES('Audit Admin','audit-admin@example.test','+234000000001','test-hash','system_administrator') RETURNING id`).Scan(&adminID); err != nil {
		t.Fatal(err)
	}
	admin := user{ID: adminID, Role: "system_administrator"}

	createBody := `{"id":"AUDIT-SUCCESS-01","label":"Audit Container","service_area":"Test District","usable_depth_cm":100,"minimum_distance_cm":5,"installation_offset_cm":0}`
	response := callAs(s.createContainer, http.MethodPost, "/api/v1/operations/containers", createBody, admin)
	if response.Code != http.StatusCreated {
		t.Fatalf("successful container creation returned %d: %s", response.Code, response.Body.String())
	}
	assertAuditRow(t, pool, "container_created", "container", "AUDIT-SUCCESS-01", adminID)

	// Force only the audit insert to fail. The container insert must roll back too.
	_, err := pool.Exec(ctx, `CREATE FUNCTION reject_audit_insert() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'audit insert intentionally rejected'; END $$`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `CREATE TRIGGER reject_audit_insert BEFORE INSERT ON audit_logs FOR EACH ROW EXECUTE FUNCTION reject_audit_insert()`)
	if err != nil {
		t.Fatal(err)
	}
	failingBody := strings.Replace(createBody, "AUDIT-SUCCESS-01", "AUDIT-ROLLBACK-01", 1)
	response = callAs(s.createContainer, http.MethodPost, "/api/v1/operations/containers", failingBody, admin)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("audit failure returned %d, want 500: %s", response.Code, response.Body.String())
	}
	var containerCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM containers WHERE id='AUDIT-ROLLBACK-01'`).Scan(&containerCount); err != nil || containerCount != 0 {
		t.Fatalf("business change remained after audit failure: count=%d err=%v", containerCount, err)
	}
	if _, err := pool.Exec(ctx, `DROP TRIGGER reject_audit_insert ON audit_logs; DROP FUNCTION reject_audit_insert()`); err != nil {
		t.Fatal(err)
	}

	// A business constraint failure must not create a second audit event.
	response = callAs(s.createContainer, http.MethodPost, "/api/v1/operations/containers", createBody, admin)
	if response.Code != http.StatusConflict {
		t.Fatalf("duplicate container returned %d, want 409", response.Code)
	}
	var duplicateAuditCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE action='container_created' AND resource_id='AUDIT-SUCCESS-01'`).Scan(&duplicateAuditCount); err != nil || duplicateAuditCount != 1 {
		t.Fatalf("failed business operation created a misleading audit event: count=%d err=%v", duplicateAuditCount, err)
	}

	// The staff update path already used a transaction; keep its audit insert atomic.
	var staffID int64
	if err := pool.QueryRow(ctx, `INSERT INTO users(full_name,email,phone,password_hash,role) VALUES('Audit Target','audit-target@example.test','+234000000002','test-hash','operations_manager') RETURNING id`).Scan(&staffID); err != nil {
		t.Fatal(err)
	}
	patch := callAs(s.updateStaff, http.MethodPatch, fmt.Sprintf("/api/v1/admin/users/%d", staffID), `{"role":"system_administrator","active":true}`, admin)
	if patch.Code != http.StatusOK {
		t.Fatalf("transaction-based staff update returned %d: %s", patch.Code, patch.Body.String())
	}
	assertAuditRow(t, pool, "staff_access_updated", "user", fmt.Sprint(staffID), adminID)

	// Passwords may never appear in details, while useful non-secret account changes do.
	password := "AUDIT-SECRET-PASSWORD-SHOULD-NEVER-APPEAR"
	body := fmt.Sprintf(`{"full_name":"Created Staff","email":"created-staff@example.test","phone":"+234000000003","password":%q,"role":"operations_manager"}`, password)
	created := callAs(s.createStaff, http.MethodPost, "/api/v1/admin/users", body, admin)
	if created.Code != http.StatusCreated {
		t.Fatalf("staff creation returned %d: %s", created.Code, created.Body.String())
	}
	var details string
	var staffResource string
	var actor int64
	var createdAt string
	if err := pool.QueryRow(ctx, `SELECT actor_id,resource_id,details::text,created_at::text FROM audit_logs WHERE action='staff_created' ORDER BY id DESC LIMIT 1`).Scan(&actor, &staffResource, &details, &createdAt); err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(details), &payload); err != nil {
		t.Fatal(err)
	}
	if actor != adminID || staffResource == "" || createdAt == "" || payload["role"] != "operations_manager" || strings.Contains(details, password) || strings.Contains(details, "created-staff@example.test") {
		t.Fatalf("staff audit metadata was incomplete or included the password: actor=%d resource=%q created=%q details=%s", actor, staffResource, createdAt, details)
	}

	deviceResponse := callAs(s.createDevice, http.MethodPost, "/api/v1/admin/devices", `{"id":"AUDIT-DEVICE-01","container_id":"AUDIT-SUCCESS-01","simulated":true}`, admin)
	if deviceResponse.Code != http.StatusCreated {
		t.Fatalf("device creation returned %d: %s", deviceResponse.Code, deviceResponse.Body.String())
	}
	var devicePayload struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(deviceResponse.Body.Bytes(), &devicePayload); err != nil || devicePayload.Token == "" {
		t.Fatalf("device creation did not return the one-time token: err=%v", err)
	}
	var deviceAuditDetails string
	if err := pool.QueryRow(ctx, `SELECT details::text FROM audit_logs WHERE action='device_created' AND resource_id='AUDIT-DEVICE-01'`).Scan(&deviceAuditDetails); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(deviceAuditDetails, devicePayload.Token) || strings.Contains(deviceAuditDetails, "token_hash") || !strings.Contains(deviceAuditDetails, "simulated") {
		t.Fatalf("device audit payload leaked a secret or omitted useful details: %s", deviceAuditDetails)
	}
}

func testingDatabaseURL(t *testing.T) string {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("DWMP_TEST_DATABASE_URL"))
	if dsn == "" {
		t.Skip("set DWMP_TEST_DATABASE_URL to an isolated PostgreSQL database whose name contains 'test'")
	}
	return dsn
}

func assertAuditRow(t *testing.T, pool *pgxpool.Pool, action, resourceType, resourceID string, actorID int64) {
	t.Helper()
	var actor int64
	var createdAt string
	if err := pool.QueryRow(context.Background(), `SELECT actor_id,created_at::text FROM audit_logs WHERE action=$1 AND resource_type=$2 AND resource_id=$3 ORDER BY id DESC LIMIT 1`, action, resourceType, resourceID).Scan(&actor, &createdAt); err != nil {
		t.Fatalf("missing audit record for %s %s/%s: %v", action, resourceType, resourceID, err)
	}
	if actor != actorID || createdAt == "" {
		t.Fatalf("audit metadata is incomplete: actor=%d created_at=%q", actor, createdAt)
	}
}
