package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

func (s *server) operationsComplaints(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	query := `SELECT c.reference,c.category,c.description,c.location,c.status,c.created_at::text,u.full_name,u.email,u.phone
		FROM complaints c JOIN users u ON u.id=c.citizen_id WHERE c.deleted_at IS NULL`
	args := []any{}
	if status != "" {
		if !oneOf(status, "submitted", "under_review", "assigned", "in_progress", "resolved", "rejected") {
			writeError(w, http.StatusBadRequest, "invalid_status_filter", "Status filter is invalid")
			return
		}
		query += ` AND c.status=$1`
		args = append(args, status)
	}
	query += ` ORDER BY c.created_at DESC LIMIT 200`
	rows, err := s.db.Query(r.Context(), query, args...)
	if err != nil {
		writeError(w, 500, "complaints_unavailable", "Complaints could not be loaded")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var c complaint
		var name, email, phone string
		if err := rows.Scan(&c.Reference, &c.Category, &c.Description, &c.Location, &c.Status, &c.CreatedAt, &name, &email, &phone); err != nil {
			writeError(w, 500, "complaints_unavailable", "Complaints could not be loaded")
			return
		}
		items = append(items, map[string]any{"complaint": c, "citizen": map[string]string{"full_name": name, "email": email, "phone": phone}})
	}
	if rows.Err() != nil {
		writeError(w, 500, "complaints_unavailable", "Complaints could not be loaded")
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (s *server) operationsPayments(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(r.Context(), `SELECT p.reference,p.amount_kobo,p.status,p.mode,p.created_at::text,u.full_name,u.email,u.phone,COALESCE(l.label,''),COALESCE(l.address_line1,''),COALESCE(l.address_line2,''),COALESCE(l.locality,''),COALESCE(l.city,''),COALESCE(l.state,'') FROM payments p JOIN users u ON u.id=p.citizen_id LEFT JOIN resident_locations l ON l.id=p.location_id WHERE u.deleted_at IS NULL ORDER BY p.created_at DESC LIMIT 300`)
	if err != nil {
		writeError(w, 500, "payments_unavailable", "Resident payments could not be loaded")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var ref, status, mode, created, name, email, phone, label, line1, line2, locality, city, state string
		var amount int64
		if err := rows.Scan(&ref, &amount, &status, &mode, &created, &name, &email, &phone, &label, &line1, &line2, &locality, &city, &state); err != nil {
			writeError(w, 500, "payments_unavailable", "Resident payments could not be loaded")
			return
		}
		address := strings.Join(nonEmpty(line1, line2, locality, city, state), ", ")
		items = append(items, map[string]any{"reference": ref, "amount_kobo": amount, "status": status, "mode": mode, "created_at": created, "resident": map[string]string{"full_name": name, "email": email, "phone": phone}, "location": map[string]string{"label": label, "address": address}})
	}
	if rows.Err() != nil {
		writeError(w, 500, "payments_unavailable", "Resident payments could not be loaded")
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func nonEmpty(parts ...string) []string {
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			out = append(out, strings.TrimSpace(part))
		}
	}
	return out
}

func (s *server) updateComplaintStatus(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Status        string `json:"status"`
		PublicMessage string `json:"public_message"`
		InternalNote  string `json:"internal_note"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.PublicMessage = strings.TrimSpace(input.PublicMessage)
	input.InternalNote = strings.TrimSpace(input.InternalNote)
	if !oneOf(input.Status, "under_review", "assigned", "in_progress", "resolved", "rejected") || len(input.PublicMessage) > 1000 || len(input.InternalNote) > 2000 {
		writeError(w, 422, "invalid_status_update", "Choose a valid status and keep notes within the allowed length")
		return
	}
	current := r.Context().Value(userContextKey).(user)
	ref := r.PathValue("reference")
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "status_update_failed", "Status could not be updated")
		return
	}
	defer tx.Rollback(r.Context())
	var previous string
	err = tx.QueryRow(r.Context(), `UPDATE complaints SET status=$1,updated_at=now() WHERE reference=$2 AND deleted_at IS NULL RETURNING status`, input.Status, ref).Scan(&previous)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "complaint_not_found", "Complaint was not found")
		return
	}
	if err != nil {
		writeError(w, 500, "status_update_failed", "Status could not be updated")
		return
	}
	_, err = tx.Exec(r.Context(), `INSERT INTO complaint_status_history(complaint_reference,status,changed_by,public_message,internal_note) VALUES($1,$2,$3,$4,$5)`, ref, input.Status, current.ID, input.PublicMessage, input.InternalNote)
	if err != nil {
		writeError(w, 500, "status_update_failed", "Status history could not be saved")
		return
	}
	_, err = tx.Exec(r.Context(), `INSERT INTO audit_logs(actor_id,action,resource_type,resource_id,details) VALUES($1,'complaint_status_updated','complaint',$2,jsonb_build_object('status',$3))`, current.ID, ref, input.Status)
	if err != nil {
		writeError(w, 500, "status_update_failed", "Audit record could not be saved")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "status_update_failed", "Status could not be updated")
		return
	}
	writeJSON(w, 200, map[string]string{"reference": ref, "status": input.Status})
}

func (s *server) listContainers(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(r.Context(), `SELECT c.id,c.label,c.service_area,c.usable_depth_cm,c.minimum_distance_cm,c.installation_offset_cm,
		r.fill_percent,r.level,r.measured_at::text FROM containers c LEFT JOIN LATERAL (SELECT fill_percent,level,measured_at FROM sensor_readings WHERE device_id IN (SELECT id FROM devices WHERE container_id=c.id) ORDER BY measured_at DESC LIMIT 1) r ON true ORDER BY c.label`)
	if err != nil {
		writeError(w, 500, "containers_unavailable", "Containers could not be loaded")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id, label, area string
		var depth, minDistance, offset float64
		var fill *float64
		var level, measuredAt *string
		if err := rows.Scan(&id, &label, &area, &depth, &minDistance, &offset, &fill, &level, &measuredAt); err != nil {
			writeError(w, 500, "containers_unavailable", "Containers could not be loaded")
			return
		}
		items = append(items, map[string]any{"id": id, "label": label, "service_area": area, "usable_depth_cm": depth, "minimum_distance_cm": minDistance, "installation_offset_cm": offset, "latest_fill_percent": fill, "latest_level": level, "last_reading_at": measuredAt})
	}
	if rows.Err() != nil {
		writeError(w, 500, "containers_unavailable", "Containers could not be loaded")
		return
	}
	writeJSON(w, 200, map[string]any{"containers": items})
}

func (s *server) createContainer(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID          string  `json:"id"`
		Label       string  `json:"label"`
		ServiceArea string  `json:"service_area"`
		Depth       float64 `json:"usable_depth_cm"`
		Minimum     float64 `json:"minimum_distance_cm"`
		Offset      float64 `json:"installation_offset_cm"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	in.ID = strings.TrimSpace(in.ID)
	in.Label = strings.TrimSpace(in.Label)
	in.ServiceArea = strings.TrimSpace(in.ServiceArea)
	if in.ID == "" {
		ref, e := complaintReference()
		if e != nil {
			writeError(w, 500, "id_generation_failed", "Container could not be registered")
			return
		}
		in.ID = "SK-" + strings.TrimPrefix(ref, "DW-")
	}
	if len(in.ID) > 40 || len(in.Label) < 2 || len(in.Label) > 120 || len(in.ServiceArea) < 2 || len(in.ServiceArea) > 120 || in.Depth <= 0 || in.Minimum < 0 || in.Minimum >= in.Depth || in.Offset < 0 || in.Offset > in.Depth/2 {
		writeError(w, 422, "invalid_container", "Check the container label, area, and sensor dimensions")
		return
	}
	current := r.Context().Value(userContextKey).(user)
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "container_creation_failed", "Container could not be registered")
		return
	}
	defer tx.Rollback(r.Context())
	var id string
	err = tx.QueryRow(r.Context(), `INSERT INTO containers(id,label,service_area,usable_depth_cm,minimum_distance_cm,installation_offset_cm) VALUES($1,$2,$3,$4,$5,$6) RETURNING id`, in.ID, in.Label, in.ServiceArea, in.Depth, in.Minimum, in.Offset).Scan(&id)
	if err != nil {
		writeError(w, 409, "container_conflict", "Container ID is already in use or its data is invalid")
		return
	}
	if err := s.commitAudit(r.Context(), tx, current.ID, "container_created", "container", id, nil); err != nil {
		writeError(w, 500, "container_creation_failed", "Container and audit record could not be saved")
		return
	}
	writeJSON(w, 201, map[string]string{"id": id})
}

func (s *server) listCollections(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(r.Context(), `SELECT j.id,j.container_id,c.label,coalesce(j.assigned_to,0),coalesce(u.full_name,'Unassigned'),j.scheduled_at::text,j.status,j.completion_note FROM collection_jobs j JOIN containers c ON c.id=j.container_id LEFT JOIN users u ON u.id=j.assigned_to ORDER BY j.scheduled_at DESC LIMIT 200`)
	if err != nil {
		writeError(w, 500, "collections_unavailable", "Collection jobs could not be loaded")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id, assigned int64
		var container, label, assignee, scheduled, status, note string
		if err := rows.Scan(&id, &container, &label, &assigned, &assignee, &scheduled, &status, &note); err != nil {
			writeError(w, 500, "collections_unavailable", "Collection jobs could not be loaded")
			return
		}
		items = append(items, map[string]any{"id": id, "container_id": container, "container_label": label, "assigned_to": assigned, "assigned_name": assignee, "scheduled_at": scheduled, "status": status, "completion_note": note})
	}
	if rows.Err() != nil {
		writeError(w, 500, "collections_unavailable", "Collection jobs could not be loaded")
		return
	}
	writeJSON(w, 200, map[string]any{"collections": items})
}

func (s *server) createCollection(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ContainerID string    `json:"container_id"`
		AssignedTo  int64     `json:"assigned_to"`
		ScheduledAt time.Time `json:"scheduled_at"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.ContainerID == "" || in.ScheduledAt.IsZero() || in.ScheduledAt.Before(time.Now().Add(-24*time.Hour)) {
		writeError(w, 422, "invalid_collection", "Choose a container and a valid collection time")
		return
	}
	if in.AssignedTo < 0 {
		writeError(w, 422, "invalid_assignee", "Choose an active operations staff member or leave the collection unassigned")
		return
	}
	if in.AssignedTo > 0 {
		var eligible bool
		if err := s.db.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1 AND role='operations_manager' AND deleted_at IS NULL)`, in.AssignedTo).Scan(&eligible); err != nil {
			writeError(w, 500, "assignee_unavailable", "The assigned operations staff member could not be verified")
			return
		}
		if !eligible {
			writeError(w, 422, "invalid_assignee", "Choose an active operations staff member or leave the collection unassigned")
			return
		}
	}
	current := r.Context().Value(userContextKey).(user)
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "collection_creation_failed", "Collection job could not be created")
		return
	}
	defer tx.Rollback(r.Context())
	var id int64
	err = tx.QueryRow(r.Context(), `INSERT INTO collection_jobs(container_id,assigned_to,scheduled_at,status,created_by) VALUES($1,nullif($2,0),$3,CASE WHEN $2=0 THEN 'scheduled' ELSE 'assigned' END,$4) RETURNING id`, in.ContainerID, in.AssignedTo, in.ScheduledAt.UTC(), current.ID).Scan(&id)
	if err != nil {
		writeError(w, 422, "collection_creation_failed", "Collection job could not be created; check the container and assigned staff member")
		return
	}
	if err := s.commitAudit(r.Context(), tx, current.ID, "collection_created", "collection_job", strconv.FormatInt(id, 10), map[string]any{"container_id": in.ContainerID, "assigned_to": in.AssignedTo, "scheduled_at": in.ScheduledAt.UTC()}); err != nil {
		writeError(w, 500, "collection_creation_failed", "Collection job and audit record could not be saved")
		return
	}
	writeJSON(w, 201, map[string]any{"id": id})
}

func (s *server) updateCollection(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, 400, "invalid_id", "Collection ID is invalid")
		return
	}
	var in struct {
		Status         string `json:"status"`
		CompletionNote string `json:"completion_note"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if !oneOf(in.Status, "assigned", "in_progress", "completed", "cancelled") || len(in.CompletionNote) > 1000 {
		writeError(w, 422, "invalid_collection_status", "Choose a valid collection status")
		return
	}
	current := r.Context().Value(userContextKey).(user)
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "collection_update_failed", "Collection could not be updated")
		return
	}
	defer tx.Rollback(r.Context())
	var updated int64
	err = tx.QueryRow(r.Context(), `UPDATE collection_jobs SET status=$1,completion_note=$2,updated_at=now() WHERE id=$3 RETURNING id`, in.Status, strings.TrimSpace(in.CompletionNote), id).Scan(&updated)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "collection_not_found", "Collection was not found")
		return
	}
	if err != nil {
		writeError(w, 500, "collection_update_failed", "Collection could not be updated")
		return
	}
	if err := s.commitAudit(r.Context(), tx, current.ID, "collection_updated", "collection_job", strconv.FormatInt(id, 10), map[string]string{"status": in.Status}); err != nil {
		writeError(w, 500, "collection_update_failed", "Collection and audit record could not be saved")
		return
	}
	writeJSON(w, 200, map[string]any{"id": updated, "status": in.Status})
}

func (s *server) listInspections(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(r.Context(), `SELECT i.id,i.container_id,c.label,i.inspector_id,u.full_name,i.condition,i.notes,i.inspected_at::text FROM inspections i JOIN containers c ON c.id=i.container_id JOIN users u ON u.id=i.inspector_id ORDER BY i.inspected_at DESC LIMIT 200`)
	if err != nil {
		writeError(w, 500, "inspections_unavailable", "Inspections could not be loaded")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id, inspector int64
		var container, label, name, condition, notes, at string
		if err := rows.Scan(&id, &container, &label, &inspector, &name, &condition, &notes, &at); err != nil {
			writeError(w, 500, "inspections_unavailable", "Inspections could not be loaded")
			return
		}
		items = append(items, map[string]any{"id": id, "container_id": container, "container_label": label, "inspector_id": inspector, "inspector": name, "condition": condition, "notes": notes, "inspected_at": at})
	}
	if rows.Err() != nil {
		writeError(w, 500, "inspections_unavailable", "Inspections could not be loaded")
		return
	}
	writeJSON(w, 200, map[string]any{"inspections": items})
}

func (s *server) createInspection(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ContainerID string `json:"container_id"`
		Condition   string `json:"condition"`
		Notes       string `json:"notes"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	in.ContainerID = strings.TrimSpace(in.ContainerID)
	in.Notes = strings.TrimSpace(in.Notes)
	if in.ContainerID == "" || !oneOf(in.Condition, "good", "needs_attention", "damaged", "missing") || len(in.Notes) > 2000 {
		writeError(w, 422, "invalid_inspection", "Provide a container, condition, and a short note")
		return
	}
	current := r.Context().Value(userContextKey).(user)
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "inspection_save_failed", "Inspection could not be saved")
		return
	}
	defer tx.Rollback(r.Context())
	var id int64
	err = tx.QueryRow(r.Context(), `INSERT INTO inspections(container_id,inspector_id,condition,notes) VALUES($1,$2,$3,$4) RETURNING id`, in.ContainerID, current.ID, in.Condition, in.Notes).Scan(&id)
	if err != nil {
		writeError(w, 422, "inspection_save_failed", "Inspection could not be saved")
		return
	}
	if err := s.commitAudit(r.Context(), tx, current.ID, "inspection_created", "inspection", strconv.FormatInt(id, 10), map[string]any{"container_id": in.ContainerID, "condition": in.Condition}); err != nil {
		writeError(w, 500, "inspection_save_failed", "Inspection and audit record could not be saved")
		return
	}
	writeJSON(w, 201, map[string]any{"id": id})
}

func (s *server) operationsStaff(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(r.Context(), `SELECT id,full_name FROM users WHERE role='operations_manager' AND deleted_at IS NULL ORDER BY full_name`)
	if err != nil {
		writeError(w, 500, "staff_unavailable", "Operations staff could not be loaded")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			writeError(w, 500, "staff_unavailable", "Operations staff could not be loaded")
			return
		}
		items = append(items, map[string]any{"id": id, "full_name": name})
	}
	if rows.Err() != nil {
		writeError(w, 500, "staff_unavailable", "Operations staff could not be loaded")
		return
	}
	writeJSON(w, 200, map[string]any{"staff": items})
}

func (s *server) operationsAnalytics(w http.ResponseWriter, r *http.Request) {
	var containers, attention, active, overdue, completed int64
	var offlineMinutes int
	if err := s.db.QueryRow(r.Context(), `SELECT COALESCE((SELECT (value::text)::int FROM application_settings WHERE key='offline_after_minutes'),180)`).Scan(&offlineMinutes); err != nil {
		writeError(w, 500, "analytics_unavailable", "Operational settings could not be loaded")
		return
	}
	if offlineMinutes < 5 || offlineMinutes > 10080 {
		offlineMinutes = 180
	}
	err := s.db.QueryRow(r.Context(), `SELECT (SELECT count(*) FROM containers),(SELECT count(*) FROM devices d WHERE d.enabled=true AND NOT EXISTS(SELECT 1 FROM sensor_readings sr WHERE sr.device_id=d.id AND sr.measured_at>now()-make_interval(mins=>$1::int))),(SELECT count(*) FROM complaints WHERE deleted_at IS NULL AND status NOT IN ('resolved','rejected')),(SELECT count(*) FROM collection_jobs WHERE scheduled_at<now() AND status NOT IN ('completed','cancelled')),(SELECT count(*) FROM collection_jobs WHERE status='completed' AND updated_at>now()-interval '30 days')`, offlineMinutes).Scan(&containers, &attention, &active, &overdue, &completed)
	if err != nil {
		writeError(w, 500, "analytics_unavailable", "Operational metrics could not be loaded")
		return
	}
	weeks := make([]map[string]any, 0, 8)
	rows, err := s.db.Query(r.Context(), `WITH periods AS (
		SELECT date_trunc('week', now()) - (n * interval '1 week') AS start_at
		FROM generate_series(7,0,-1) AS n
	), complaint_counts AS (
		SELECT p.start_at,
			(SELECT count(*) FROM complaints c WHERE c.deleted_at IS NULL AND c.created_at >= p.start_at AND c.created_at < p.start_at + interval '1 week') AS opened,
			(SELECT count(DISTINCT h.complaint_reference) FROM complaint_status_history h JOIN complaints c ON c.reference=h.complaint_reference WHERE c.deleted_at IS NULL AND h.status='resolved' AND h.created_at >= p.start_at AND h.created_at < p.start_at + interval '1 week') AS resolved,
			(SELECT count(*) FROM collection_jobs j WHERE j.status='completed' AND j.updated_at >= p.start_at AND j.updated_at < p.start_at + interval '1 week') AS collections
		FROM periods p
	) SELECT start_at::date::text,opened,resolved,collections FROM complaint_counts ORDER BY start_at`)
	if err != nil {
		writeError(w, 500, "analytics_unavailable", "Operational trends could not be loaded")
		return
	}
	defer rows.Close()
	for rows.Next() {
		var week string
		var opened, resolved, collections int64
		if err := rows.Scan(&week, &opened, &resolved, &collections); err != nil {
			writeError(w, 500, "analytics_unavailable", "Operational trends could not be loaded")
			return
		}
		weeks = append(weeks, map[string]any{"week": week, "complaints_opened": opened, "complaints_resolved": resolved, "collections_completed": collections})
	}
	if rows.Err() != nil {
		writeError(w, 500, "analytics_unavailable", "Operational trends could not be loaded")
		return
	}
	var averageResolutionHours *float64
	err = s.db.QueryRow(r.Context(), `SELECT avg(EXTRACT(epoch FROM (h.created_at-c.created_at))/3600.0)
		FROM complaints c JOIN LATERAL (SELECT min(created_at) AS created_at FROM complaint_status_history WHERE complaint_reference=c.reference AND status='resolved') h ON h.created_at IS NOT NULL
		WHERE c.deleted_at IS NULL AND h.created_at >= now()-interval '30 days'`).Scan(&averageResolutionHours)
	if err != nil {
		writeError(w, 500, "analytics_unavailable", "Resolution metrics could not be loaded")
		return
	}
	writeJSON(w, 200, map[string]any{"registered_containers": containers, "offline_devices": attention, "active_complaints": active, "overdue_collections": overdue, "completed_collections_30d": completed, "average_resolution_hours_30d": averageResolutionHours, "weekly_trends": weeks})
}

func (s *server) publicAnnouncements(w http.ResponseWriter, r *http.Request) {
	s.announcements(w, r, true)
}
func (s *server) listAnnouncements(w http.ResponseWriter, r *http.Request) {
	s.announcements(w, r, false)
}
func (s *server) announcements(w http.ResponseWriter, r *http.Request, publicOnly bool) {
	query := `SELECT a.id,a.title,a.body,a.status,a.published_at::text,a.created_at::text,u.full_name FROM announcements a JOIN users u ON u.id=a.author_id`
	if publicOnly {
		query += ` WHERE a.status='published' AND a.published_at<=now()`
	}
	query += ` ORDER BY coalesce(a.published_at,a.created_at) DESC LIMIT 100`
	rows, err := s.db.Query(r.Context(), query)
	if err != nil {
		writeError(w, 500, "announcements_unavailable", "Announcements could not be loaded")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id int64
		var title, body, status, created, author string
		var published *string
		if err := rows.Scan(&id, &title, &body, &status, &published, &created, &author); err != nil {
			writeError(w, 500, "announcements_unavailable", "Announcements could not be loaded")
			return
		}
		items = append(items, map[string]any{"id": id, "title": title, "body": body, "status": status, "published_at": published, "created_at": created, "author": author})
	}
	if rows.Err() != nil {
		writeError(w, 500, "announcements_unavailable", "Announcements could not be loaded")
		return
	}
	writeJSON(w, 200, map[string]any{"announcements": items})
}

func (s *server) createAnnouncement(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Title  string `json:"title"`
		Body   string `json:"body"`
		Status string `json:"status"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Title = strings.TrimSpace(in.Title)
	in.Body = strings.TrimSpace(in.Body)
	if in.Status == "" {
		in.Status = "draft"
	}
	if len(in.Title) < 3 || len(in.Title) > 180 || len(in.Body) < 10 || len(in.Body) > 10000 || !oneOf(in.Status, "draft", "published") {
		writeError(w, 422, "invalid_announcement", "Provide a title, message, and valid publication status")
		return
	}
	current := r.Context().Value(userContextKey).(user)
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "announcement_save_failed", "Announcement could not be saved")
		return
	}
	defer tx.Rollback(r.Context())
	var id int64
	err = tx.QueryRow(r.Context(), `INSERT INTO announcements(title,body,status,author_id,published_at) VALUES($1,$2,$3,$4,CASE WHEN $3='published' THEN now() ELSE NULL END) RETURNING id`, in.Title, in.Body, in.Status, current.ID).Scan(&id)
	if err != nil {
		writeError(w, 500, "announcement_save_failed", "Announcement could not be saved")
		return
	}
	if err := s.commitAudit(r.Context(), tx, current.ID, "announcement_created", "announcement", strconv.FormatInt(id, 10), map[string]string{"status": in.Status}); err != nil {
		writeError(w, 500, "announcement_save_failed", "Announcement and audit record could not be saved")
		return
	}
	writeJSON(w, 201, map[string]any{"id": id, "status": in.Status})
}

func (s *server) listNotifications(w http.ResponseWriter, r *http.Request) {
	current := r.Context().Value(userContextKey).(user)
	rows, err := s.db.Query(r.Context(), `SELECT id,title,message,resource_type,resource_id,read_at::text,created_at::text FROM notifications WHERE target_role=$1 ORDER BY created_at DESC LIMIT 100`, current.Role)
	if err != nil {
		writeError(w, 500, "notifications_unavailable", "Notifications could not be loaded")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id int64
		var title, message, kind, resource, created string
		var readAt *string
		if err := rows.Scan(&id, &title, &message, &kind, &resource, &readAt, &created); err != nil {
			writeError(w, 500, "notifications_unavailable", "Notifications could not be loaded")
			return
		}
		items = append(items, map[string]any{"id": id, "title": title, "message": message, "resource_type": kind, "resource_id": resource, "read_at": readAt, "created_at": created})
	}
	if rows.Err() != nil {
		writeError(w, 500, "notifications_unavailable", "Notifications could not be loaded")
		return
	}
	writeJSON(w, 200, map[string]any{"notifications": items})
}

func (s *server) writeAudit(ctx context.Context, tx pgx.Tx, actor int64, action, kind, id string, details any) error {
	if details == nil {
		details = map[string]any{}
	}
	encoded, err := json.Marshal(details)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO audit_logs(actor_id,action,resource_type,resource_id,details) VALUES($1,$2,$3,$4,$5::jsonb)`, actor, action, kind, id, string(encoded))
	return err
}

func (s *server) commitAudit(ctx context.Context, tx pgx.Tx, actor int64, action, kind, id string, details any) error {
	if err := s.writeAudit(ctx, tx, actor, action, kind, id, details); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

var _ = pgx.ErrNoRows
