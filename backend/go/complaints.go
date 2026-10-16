package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
)

type complaint struct {
	Reference   string `json:"reference"`
	Category    string `json:"category"`
	Description string `json:"description"`
	Location    string `json:"location"`
	Status      string `json:"status"`
	CreatedAt   string `json:"created_at"`
}

func (s *server) createComplaint(w http.ResponseWriter, r *http.Request) {
	current := r.Context().Value(userContextKey).(user)
	var input struct {
		Category    string `json:"category"`
		Description string `json:"description"`
		Location    string `json:"location"`
		LocationID  int64  `json:"location_id"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Category = strings.TrimSpace(input.Category)
	input.Description = strings.TrimSpace(input.Description)
	input.Location = strings.TrimSpace(input.Location)
	if !oneOf(input.Category, "overflowing_skip", "missed_collection", "illegal_dumping", "other") || len(input.Description) < 10 || len(input.Description) > 4000 || input.LocationID < 1 {
		writeError(w, http.StatusUnprocessableEntity, "invalid_complaint", "Choose a category, describe the issue and select a saved service address")
		return
	}
	var saved residentLocation
	err := s.db.QueryRow(r.Context(), `SELECT id,label,address_line1,address_line2,locality,city,state,postal_code,latitude,longitude FROM resident_locations WHERE id=$1 AND resident_id=$2 AND deleted_at IS NULL`, input.LocationID, current.ID).Scan(&saved.ID, &saved.Label, &saved.AddressLine1, &saved.AddressLine2, &saved.Locality, &saved.City, &saved.State, &saved.PostalCode, &saved.Latitude, &saved.Longitude)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "location_not_found", "Select one of your saved addresses")
		return
	}
	if err != nil {
		writeError(w, 500, "locations_unavailable", "Saved address could not be loaded")
		return
	}
	input.Location = locationText(saved)
	ref, err := complaintReference()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "reference_failed", "Complaint could not be created")
		return
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "complaint_creation_failed", "Complaint could not be saved")
		return
	}
	defer tx.Rollback(r.Context())
	var created complaint
	err = tx.QueryRow(r.Context(), `INSERT INTO complaints(reference,citizen_id,category,description,location,location_id,status)
		VALUES($1,$2,$3,$4,$5,$6,'submitted') RETURNING reference,category,description,location,status,created_at`, ref, current.ID, input.Category, input.Description, input.Location, input.LocationID).
		Scan(&created.Reference, &created.Category, &created.Description, &created.Location, &created.Status, &created.CreatedAt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "complaint_creation_failed", "Complaint could not be saved")
		return
	}
	_, err = tx.Exec(r.Context(), `INSERT INTO complaint_status_history(complaint_reference,status,changed_by,public_message)
		VALUES($1,'submitted',$2,'Your complaint has been received.')`, created.Reference, current.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "complaint_creation_failed", "Complaint history could not be saved")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "complaint_creation_failed", "Complaint could not be saved")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"complaint": created})
}

func (s *server) listComplaints(w http.ResponseWriter, r *http.Request) {
	current := r.Context().Value(userContextKey).(user)
	rows, err := s.db.Query(r.Context(), `SELECT reference,category,description,location,status,created_at::text FROM complaints WHERE citizen_id=$1 AND deleted_at IS NULL ORDER BY created_at DESC LIMIT 100`, current.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "complaints_unavailable", "Complaints could not be loaded")
		return
	}
	defer rows.Close()
	items := make([]complaint, 0)
	for rows.Next() {
		var item complaint
		if err := rows.Scan(&item.Reference, &item.Category, &item.Description, &item.Location, &item.Status, &item.CreatedAt); err != nil {
			writeError(w, http.StatusInternalServerError, "complaints_unavailable", "Complaints could not be loaded")
			return
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		writeError(w, http.StatusInternalServerError, "complaints_unavailable", "Complaints could not be loaded")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"complaints": items})
}

func (s *server) getComplaint(w http.ResponseWriter, r *http.Request) {
	current := r.Context().Value(userContextKey).(user)
	ref := r.PathValue("reference")
	var item complaint
	err := s.db.QueryRow(r.Context(), `SELECT reference,category,description,location,status,created_at::text FROM complaints WHERE reference=$1 AND citizen_id=$2 AND deleted_at IS NULL`, ref, current.ID).
		Scan(&item.Reference, &item.Category, &item.Description, &item.Location, &item.Status, &item.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "complaint_not_found", "Complaint was not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "complaint_unavailable", "Complaint could not be loaded")
		return
	}
	rows, err := s.db.Query(r.Context(), `SELECT status,public_message,created_at::text FROM complaint_status_history WHERE complaint_reference=$1 ORDER BY created_at`, ref)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "complaint_unavailable", "Complaint history could not be loaded")
		return
	}
	defer rows.Close()
	history := make([]map[string]string, 0)
	for rows.Next() {
		var status, note, at string
		if err := rows.Scan(&status, &note, &at); err != nil {
			writeError(w, http.StatusInternalServerError, "complaint_unavailable", "Complaint history could not be loaded")
			return
		}
		history = append(history, map[string]string{"status": status, "note": note, "created_at": at})
	}
	if rows.Err() != nil {
		writeError(w, http.StatusInternalServerError, "complaint_unavailable", "Complaint history could not be loaded")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"complaint": item, "history": history})
}

func complaintReference() (string, error) {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "DW-" + strings.ToUpper(hex.EncodeToString(b)), nil
}
func oneOf(v string, options ...string) bool {
	for _, option := range options {
		if v == option {
			return true
		}
	}
	return false
}
