package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

func (s *server) adminUsers(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(r.Context(), `SELECT id,full_name,email,phone,role,created_at::text,deleted_at IS NULL FROM users WHERE role<>'citizen' ORDER BY full_name`)
	if err != nil {
		writeError(w, 500, "staff_unavailable", "Staff accounts could not be loaded")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id int64
		var name, email, phone, role, created string
		var active bool
		if err := rows.Scan(&id, &name, &email, &phone, &role, &created, &active); err != nil {
			writeError(w, 500, "staff_unavailable", "Staff accounts could not be loaded")
			return
		}
		items = append(items, map[string]any{"id": id, "full_name": name, "email": email, "phone": phone, "role": role, "created_at": created, "active": active})
	}
	if rows.Err() != nil {
		writeError(w, 500, "staff_unavailable", "Staff accounts could not be loaded")
		return
	}
	writeJSON(w, 200, map[string]any{"staff": items})
}

func (s *server) createStaff(w http.ResponseWriter, r *http.Request) {
	var in struct {
		FullName string `json:"full_name"`
		Email    string `json:"email"`
		Phone    string `json:"phone"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	in.FullName = strings.TrimSpace(in.FullName)
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	in.Phone = strings.TrimSpace(in.Phone)
	if len(in.FullName) < 2 || len(in.FullName) > 120 || !validEmail(in.Email) || len(in.Phone) < 7 || len(in.Phone) > 32 || len(in.Password) < 14 || !oneOf(in.Role, "operations_manager", "system_administrator") {
		writeError(w, 422, "invalid_staff", "Provide valid staff details, a 14 character password, and an allowed staff role")
		return
	}
	hash, err := hashPassword(in.Password)
	if err != nil {
		writeError(w, 500, "staff_creation_failed", "Staff account could not be created")
		return
	}
	current := r.Context().Value(userContextKey).(user)
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "staff_creation_failed", "Staff account could not be created")
		return
	}
	defer tx.Rollback(r.Context())
	var id int64
	err = tx.QueryRow(r.Context(), `INSERT INTO users(full_name,email,phone,password_hash,role,terms_accepted_at) VALUES($1,$2,$3,$4,$5,now()) RETURNING id`, in.FullName, in.Email, in.Phone, hash, in.Role).Scan(&id)
	if err != nil {
		writeError(w, 409, "staff_conflict", "Email is already registered or staff details are invalid")
		return
	}
	if err := s.commitAudit(r.Context(), tx, current.ID, "staff_created", "user", strconv.FormatInt(id, 10), map[string]string{"role": in.Role}); err != nil {
		writeError(w, 500, "staff_creation_failed", "Staff account and audit record could not be saved")
		return
	}
	writeJSON(w, 201, map[string]any{"id": id, "role": in.Role})
}

func (s *server) updateStaff(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, 400, "invalid_id", "Staff ID is invalid")
		return
	}
	var in struct {
		Role   string `json:"role"`
		Active bool   `json:"active"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if !oneOf(in.Role, "operations_manager", "system_administrator") {
		writeError(w, 422, "invalid_role", "Choose a valid role")
		return
	}
	current := r.Context().Value(userContextKey).(user)
	if current.ID == id && (!in.Active || in.Role != "system_administrator") {
		writeError(w, 422, "cannot_change_own_access", "You cannot disable or demote your own administrator account")
		return
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "staff_update_failed", "Staff account could not be updated")
		return
	}
	defer tx.Rollback(r.Context())
	if _, err = tx.Exec(r.Context(), `SELECT pg_advisory_xact_lock(71620418)`); err != nil {
		writeError(w, 500, "staff_update_failed", "Staff account could not be updated")
		return
	}
	var existingRole string
	var existingActive bool
	err = tx.QueryRow(r.Context(), `SELECT role,deleted_at IS NULL FROM users WHERE id=$1 AND role<>'citizen' FOR UPDATE`, id).Scan(&existingRole, &existingActive)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "staff_not_found", "Staff account was not found")
		return
	}
	if err != nil {
		writeError(w, 500, "staff_update_failed", "Staff account could not be updated")
		return
	}
	if existingRole == "system_administrator" && existingActive && (!in.Active || in.Role != "system_administrator") {
		var admins int
		if err = tx.QueryRow(r.Context(), `SELECT count(*) FROM users WHERE role='system_administrator' AND deleted_at IS NULL`).Scan(&admins); err != nil {
			writeError(w, 500, "staff_update_failed", "Administrator access could not be verified")
			return
		}
		if admins <= 1 {
			writeError(w, 422, "last_administrator", "At least one active system administrator must remain")
			return
		}
	}
	var found int64
	err = tx.QueryRow(r.Context(), `UPDATE users SET role=$1,deleted_at=CASE WHEN $2 THEN NULL ELSE now() END,session_version=session_version+CASE WHEN $2 THEN 0 ELSE 1 END,updated_at=now() WHERE id=$3 AND role<>'citizen' RETURNING id`, in.Role, in.Active, id).Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "staff_not_found", "Staff account was not found")
		return
	}
	if err != nil {
		writeError(w, 500, "staff_update_failed", "Staff account could not be updated")
		return
	}
	if err = s.writeAudit(r.Context(), tx, current.ID, "staff_access_updated", "user", strconv.FormatInt(id, 10), map[string]any{"role": in.Role, "active": in.Active}); err != nil {
		writeError(w, 500, "staff_update_failed", "Staff account or audit record could not be saved")
		return
	}
	if tx.Commit(r.Context()) != nil {
		writeError(w, 500, "staff_update_failed", "Staff account or audit record could not be saved")
		return
	}
	writeJSON(w, 200, map[string]any{"id": found, "role": in.Role, "active": in.Active})
}

func (s *server) adminDevices(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(r.Context(), `SELECT d.id,d.container_id,c.label,d.enabled,d.is_simulated,d.created_at::text,r.fill_percent,r.level,r.measured_at::text,c.usable_depth_cm,c.minimum_distance_cm,c.installation_offset_cm FROM devices d JOIN containers c ON c.id=d.container_id LEFT JOIN LATERAL(SELECT fill_percent,level,measured_at FROM sensor_readings WHERE device_id=d.id ORDER BY measured_at DESC LIMIT 1) r ON true ORDER BY d.created_at DESC`)
	if err != nil {
		writeError(w, 500, "devices_unavailable", "Devices could not be loaded")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id, container, label, created string
		var enabled, simulated bool
		var fill *float64
		var level, measured *string
		var depth, minimum, offset float64
		if err := rows.Scan(&id, &container, &label, &enabled, &simulated, &created, &fill, &level, &measured, &depth, &minimum, &offset); err != nil {
			writeError(w, 500, "devices_unavailable", "Devices could not be loaded")
			return
		}
		items = append(items, map[string]any{"id": id, "container_id": container, "container_label": label, "container_depth_cm": depth, "minimum_distance_cm": minimum, "installation_offset_cm": offset, "enabled": enabled, "simulated": simulated, "created_at": created, "latest_fill_percent": fill, "latest_level": level, "last_reading_at": measured})
	}
	if rows.Err() != nil {
		writeError(w, 500, "devices_unavailable", "Devices could not be loaded")
		return
	}
	writeJSON(w, 200, map[string]any{"devices": items})
}

func (s *server) listAreaFees(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(r.Context(), `SELECT f.id,f.area_id,f.area_name,a.locality,a.city,a.state,f.amount_kobo,f.updated_at::text
		FROM area_waste_fees f LEFT JOIN service_areas a ON a.id=f.area_id ORDER BY lower(f.area_name),f.id`)
	if err != nil {
		writeError(w, 500, "area_fees_unavailable", "Area fees could not be loaded")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id int64
		var areaID *int64
		var name, updated string
		var locality, city, state *string
		var amount int64
		if err := rows.Scan(&id, &areaID, &name, &locality, &city, &state, &amount, &updated); err != nil {
			writeError(w, 500, "area_fees_unavailable", "Area fees could not be loaded")
			return
		}
		items = append(items, map[string]any{"id": id, "area_id": areaID, "area_name": name, "locality": locality, "city": city, "state": state, "resolved": areaID != nil, "amount_kobo": amount, "updated_at": updated})
	}
	if rows.Err() != nil {
		writeError(w, 500, "area_fees_unavailable", "Area fees could not be loaded")
		return
	}
	writeJSON(w, 200, map[string]any{"area_fees": items})
}

func (s *server) saveAreaFee(w http.ResponseWriter, r *http.Request) {
	var in struct {
		AreaID     int64 `json:"area_id"`
		LegacyID   int64 `json:"legacy_fee_id"`
		AmountKobo int64 `json:"amount_kobo"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.AreaID < 1 || in.AmountKobo < 0 || in.AmountKobo > 1000000000000 || in.LegacyID < 0 {
		writeError(w, 422, "invalid_area_fee", "Choose a managed service area and enter a non-negative fee amount")
		return
	}
	current := r.Context().Value(userContextKey).(user)
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "area_fee_save_failed", "Area fee could not be saved")
		return
	}
	defer tx.Rollback(r.Context())
	var id int64
	if in.LegacyID > 0 {
		err = tx.QueryRow(r.Context(), `WITH active_area AS (
			SELECT id,locality FROM service_areas WHERE id=$4 AND is_active FOR SHARE
		) UPDATE area_waste_fees f SET area_id=a.id,area_name=a.locality,amount_kobo=$1,updated_by=$2,updated_at=now()
			FROM active_area a WHERE f.id=$3 AND f.area_id IS NULL RETURNING f.id`, in.AmountKobo, current.ID, in.LegacyID, in.AreaID).Scan(&id)
	} else {
		err = tx.QueryRow(r.Context(), `WITH active_area AS (
			SELECT id,locality FROM service_areas WHERE id=$1 AND is_active FOR SHARE
		) INSERT INTO area_waste_fees(area_id,area_name,amount_kobo,updated_by,updated_at)
			SELECT a.id,a.locality,$2,$3,now() FROM active_area a
			ON CONFLICT (area_id) WHERE area_id IS NOT NULL DO UPDATE SET area_name=excluded.area_name,amount_kobo=excluded.amount_kobo,updated_by=excluded.updated_by,updated_at=now()
			RETURNING id`, in.AreaID, in.AmountKobo, current.ID).Scan(&id)
	}
	if isUniqueViolation(err) {
		writeError(w, 409, "area_fee_exists", "A fee is already assigned to this service area; the legacy record was left unchanged")
		return
	}
	if errors.Is(err, pgx.ErrNoRows) {
		if in.LegacyID > 0 {
			writeError(w, 409, "legacy_fee_unavailable", "The legacy fee was already reconciled or the selected area is inactive")
		} else {
			writeError(w, 422, "service_area_unavailable", "Choose an existing active service area")
		}
		return
	}
	if err != nil {
		writeError(w, 500, "area_fee_save_failed", "Area fee could not be saved")
		return
	}
	if err := s.commitAudit(r.Context(), tx, current.ID, "area_fee_saved", "area_fee", strconv.FormatInt(id, 10), map[string]any{"area_id": in.AreaID, "amount_kobo": in.AmountKobo, "legacy_fee_id": in.LegacyID}); err != nil {
		writeError(w, 500, "area_fee_save_failed", "Area fee and audit record could not be saved")
		return
	}
	writeJSON(w, 200, map[string]any{"id": id, "area_id": in.AreaID, "amount_kobo": in.AmountKobo})
}

func (s *server) createDevice(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID          string `json:"id"`
		ContainerID string `json:"container_id"`
		Simulated   bool   `json:"simulated"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	in.ID = strings.TrimSpace(in.ID)
	if in.ContainerID == "" {
		writeError(w, 422, "invalid_device", "Choose a registered container")
		return
	}
	if in.Simulated && !s.development {
		writeError(w, 422, "simulator_unavailable", "Simulator devices can only be registered outside production")
		return
	}
	if in.ID == "" {
		b := make([]byte, 6)
		if _, err := rand.Read(b); err != nil {
			writeError(w, 500, "device_creation_failed", "Device could not be registered")
			return
		}
		in.ID = "DEV-" + strings.ToUpper(hex.EncodeToString(b))
	}
	if len(in.ID) > 50 {
		writeError(w, 422, "invalid_device", "Device ID is too long")
		return
	}
	secretBytes := make([]byte, 32)
	if _, err := rand.Read(secretBytes); err != nil {
		writeError(w, 500, "device_creation_failed", "Device could not be registered")
		return
	}
	secret := base64.RawURLEncoding.EncodeToString(secretBytes)
	digest := sha256.Sum256([]byte(secret))
	current := r.Context().Value(userContextKey).(user)
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "device_creation_failed", "Device could not be registered")
		return
	}
	defer tx.Rollback(r.Context())
	_, err = tx.Exec(r.Context(), `INSERT INTO devices(id,container_id,token_hash,is_simulated,enabled) VALUES($1,$2,$3,$4,true)`, in.ID, in.ContainerID, hex.EncodeToString(digest[:]), in.Simulated)
	if err != nil {
		writeError(w, 409, "device_conflict", "Device ID or container is invalid")
		return
	}
	if err := s.commitAudit(r.Context(), tx, current.ID, "device_created", "device", in.ID, map[string]any{"container_id": in.ContainerID, "simulated": in.Simulated}); err != nil {
		writeError(w, 500, "device_creation_failed", "Device and audit record could not be saved")
		return
	}
	writeJSON(w, 201, map[string]any{"id": in.ID, "token": secret, "simulated": in.Simulated, "warning": "Copy this token now. It will not be shown again."})
}

func (s *server) updateDevice(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	var in struct {
		Enabled     *bool `json:"enabled"`
		RotateToken bool  `json:"rotate_token"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if id == "" || in.Enabled == nil && !in.RotateToken {
		writeError(w, 422, "invalid_device_update", "Enable state or token rotation is required")
		return
	}
	current := r.Context().Value(userContextKey).(user)
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "device_update_failed", "Device could not be updated")
		return
	}
	defer tx.Rollback(r.Context())
	var token string
	if in.RotateToken {
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			writeError(w, 500, "device_update_failed", "Device could not be updated")
			return
		}
		token = base64.RawURLEncoding.EncodeToString(b)
		digest := sha256.Sum256([]byte(token))
		err := tx.QueryRow(r.Context(), `UPDATE devices SET token_hash=$1,enabled=coalesce($2,enabled) WHERE id=$3 RETURNING id`, hex.EncodeToString(digest[:]), in.Enabled, id).Scan(new(string))
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, 404, "device_not_found", "Device was not found")
			return
		}
		if err != nil {
			writeError(w, 500, "device_update_failed", "Device could not be updated")
			return
		}
	}
	if !in.RotateToken {
		tag, err := tx.Exec(r.Context(), `UPDATE devices SET enabled=$1 WHERE id=$2`, *in.Enabled, id)
		if err != nil || tag.RowsAffected() == 0 {
			writeError(w, 404, "device_not_found", "Device was not found")
			return
		}
	}
	if err := s.commitAudit(r.Context(), tx, current.ID, "device_updated", "device", id, map[string]any{"enabled": in.Enabled, "token_rotated": in.RotateToken}); err != nil {
		writeError(w, 500, "device_update_failed", "Device and audit record could not be saved")
		return
	}
	response := map[string]any{"id": id}
	if in.Enabled != nil {
		response["enabled"] = *in.Enabled
	}
	if token != "" {
		response["token"] = token
		response["warning"] = "Copy this token now. It will not be shown again."
	}
	writeJSON(w, 200, response)
}

func (s *server) auditLogs(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(r.Context(), `SELECT a.id,a.actor_id,coalesce(u.full_name,'System'),a.action,a.resource_type,a.resource_id,a.details::text,a.created_at::text FROM audit_logs a LEFT JOIN users u ON u.id=a.actor_id ORDER BY a.created_at DESC LIMIT 200`)
	if err != nil {
		writeError(w, 500, "audit_logs_unavailable", "Audit logs could not be loaded")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id, actor int64
		var name, action, kind, resource, details, created string
		if err := rows.Scan(&id, &actor, &name, &action, &kind, &resource, &details, &created); err != nil {
			writeError(w, 500, "audit_logs_unavailable", "Audit logs could not be loaded")
			return
		}
		items = append(items, map[string]any{"id": id, "actor_id": actor, "actor": name, "action": action, "resource_type": kind, "resource_id": resource, "details": json.RawMessage(details), "created_at": created})
	}
	if rows.Err() != nil {
		writeError(w, 500, "audit_logs_unavailable", "Audit logs could not be loaded")
		return
	}
	writeJSON(w, 200, map[string]any{"audit_logs": items})
}

func (s *server) getSettings(w http.ResponseWriter, r *http.Request) {
	values := map[string]json.RawMessage{"fill_warning_threshold": json.RawMessage("70"), "fill_critical_threshold": json.RawMessage("90"), "offline_after_minutes": json.RawMessage("180"), "waste_fee_kobo": json.RawMessage("0")}
	rows, err := s.db.Query(r.Context(), `SELECT key,value::text FROM application_settings`)
	if err != nil {
		writeError(w, 500, "settings_unavailable", "Settings could not be loaded")
		return
	}
	defer rows.Close()
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			writeError(w, 500, "settings_unavailable", "Settings could not be loaded")
			return
		}
		values[key] = json.RawMessage(value)
	}
	if rows.Err() != nil {
		writeError(w, 500, "settings_unavailable", "Settings could not be loaded")
		return
	}
	writeJSON(w, 200, map[string]any{"settings": values})
}

func (s *server) updateSettings(w http.ResponseWriter, r *http.Request) {
	var values map[string]json.RawMessage
	if !decodeJSON(w, r, &values) {
		return
	}
	if len(values) == 0 || len(values) > 10 {
		writeError(w, 422, "invalid_settings", "Settings payload is invalid")
		return
	}
	current := r.Context().Value(userContextKey).(user)
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "settings_update_failed", "Settings could not be saved")
		return
	}
	defer tx.Rollback(r.Context())
	warning, critical := 70.0, 90.0
	if err := tx.QueryRow(r.Context(), `SELECT COALESCE((SELECT (value::text)::numeric FROM application_settings WHERE key='fill_warning_threshold'),70),COALESCE((SELECT (value::text)::numeric FROM application_settings WHERE key='fill_critical_threshold'),90)`).Scan(&warning, &critical); err != nil {
		writeError(w, 500, "settings_unavailable", "Current settings could not be loaded")
		return
	}
	if raw, ok := values["fill_warning_threshold"]; ok {
		if err := json.Unmarshal(raw, &warning); err != nil {
			writeError(w, 422, "invalid_setting_value", "Warning threshold must be numeric")
			return
		}
	}
	if raw, ok := values["fill_critical_threshold"]; ok {
		if err := json.Unmarshal(raw, &critical); err != nil {
			writeError(w, 422, "invalid_setting_value", "Critical threshold must be numeric")
			return
		}
	}
	if warning <= 0 || warning >= critical || critical > 100 {
		writeError(w, 422, "invalid_thresholds", "Warning threshold must be lower than the critical threshold, and both must be at most 100")
		return
	}
	for key, raw := range values {
		if !oneOf(key, "fill_warning_threshold", "fill_critical_threshold", "offline_after_minutes", "waste_fee_kobo") {
			writeError(w, 422, "unknown_setting", "One or more settings are not supported")
			return
		}
		var number float64
		if err := json.Unmarshal(raw, &number); err != nil {
			writeError(w, 422, "invalid_setting_value", "Setting value must be numeric")
			return
		}
		if key == "waste_fee_kobo" {
			if number < 0 || number > 1000000000000 || math.Trunc(number) != number {
				writeError(w, 422, "invalid_setting_value", "Fee must be a non-negative whole number of kobo")
				return
			}
		} else if key == "offline_after_minutes" {
			if number < 5 || number > 10080 || math.Trunc(number) != number {
				writeError(w, 422, "invalid_setting_value", "Offline timeout must be between 5 minutes and 7 days")
				return
			}
		} else if number < 1 || number > 100 {
			writeError(w, 422, "invalid_setting_value", "Fill thresholds must be between 1 and 100")
			return
		}
		_, err = tx.Exec(r.Context(), `INSERT INTO application_settings(key,value,updated_by,updated_at) VALUES($1,to_jsonb($2::numeric),$3,now()) ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_by=excluded.updated_by,updated_at=now()`, key, number, current.ID)
		if err != nil {
			writeError(w, 500, "settings_update_failed", "Settings could not be saved")
			return
		}
	}
	_, err = tx.Exec(r.Context(), `INSERT INTO audit_logs(actor_id,action,resource_type,resource_id,details) VALUES($1,'settings_updated','application_settings','', $2::jsonb)`, current.ID, string(mustJSON(values)))
	if err != nil {
		writeError(w, 500, "settings_update_failed", "Audit record could not be saved")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "settings_update_failed", "Settings could not be saved")
		return
	}
	writeJSON(w, 200, map[string]any{"updated": true})
}

func mustJSON(value any) []byte { result, _ := json.Marshal(value); return result }
