package main

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

func (s *server) requestPropertyUnit(w http.ResponseWriter, r *http.Request) {
	current := r.Context().Value(userContextKey).(user)
	var in struct {
		LocationID int64  `json:"location_id"`
		Code       string `json:"code"`
		UnitType   string `json:"unit_type"`
		FloorLabel string `json:"floor_label"`
		Service    string `json:"service_scope"`
		Role       string `json:"relationship"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Code, in.FloorLabel = strings.TrimSpace(in.Code), strings.TrimSpace(in.FloorLabel)
	if in.LocationID < 1 || len(in.Code) < 1 || len(in.Code) > 80 || len(in.FloorLabel) > 80 || !oneOf(in.UnitType, "residential", "shop", "office", "shared", "other") || !oneOf(in.Service, "individual_bin", "shared_bin") || !oneOf(in.Role, "owner", "tenant", "caretaker", "agent") {
		writeError(w, 422, "invalid_unit_request", "Enter a saved address and valid unit details")
		return
	}
	var requestID int64
	err := s.db.QueryRow(r.Context(), `INSERT INTO property_unit_requests(property_id,location_id,requester_id,unit_code,unit_type,floor_label,service_scope,relationship)
		SELECT p.id,l.id,$2,$3,$4,$5,$6,$7 FROM resident_locations l JOIN service_areas a ON a.id=l.area_id AND a.is_active
		JOIN registered_properties p ON p.area_id=a.id AND p.address_key=lower(btrim(l.address_line1)) AND p.status<>'inactive' AND p.review_status IN ('awaiting_bin_delivery','verified')
		WHERE l.id=$1 AND l.resident_id=$2 AND l.deleted_at IS NULL RETURNING id`, in.LocationID, current.ID, in.Code, in.UnitType, in.FloorLabel, in.Service, in.Role).Scan(&requestID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "property_not_found", "No registered property matches this saved address in its managed area. Ask service staff to check the property record.")
		return
	}
	if isUniqueViolation(err) {
		writeError(w, 409, "unit_request_exists", "A matching unit request is already waiting for review")
		return
	}
	if err != nil {
		writeError(w, 500, "unit_request_failed", "Unit request could not be submitted")
		return
	}
	writeJSON(w, 201, map[string]int64{"id": requestID})
}

func (s *server) residentPropertyUnitRequests(w http.ResponseWriter, r *http.Request) {
	current := r.Context().Value(userContextKey).(user)
	rows, err := s.db.Query(r.Context(), `SELECT q.id,q.unit_code,q.floor_label,q.relationship,q.status,COALESCE(q.review_reason,''),q.created_at,q.reviewed_at FROM property_unit_requests q WHERE q.requester_id=$1 ORDER BY q.created_at DESC LIMIT 100`, current.ID)
	if err != nil {
		writeError(w, 500, "unit_requests_unavailable", "Your unit requests could not be loaded")
		return
	}
	defer rows.Close()
	type requestView struct {
		ID           int64      `json:"id"`
		Code         string     `json:"code"`
		Floor        string     `json:"floor_label"`
		Relationship string     `json:"relationship"`
		Status       string     `json:"status"`
		Reason       string     `json:"review_reason"`
		CreatedAt    time.Time  `json:"created_at"`
		ReviewedAt   *time.Time `json:"reviewed_at,omitempty"`
	}
	items := make([]requestView, 0)
	for rows.Next() {
		var item requestView
		if err := rows.Scan(&item.ID, &item.Code, &item.Floor, &item.Relationship, &item.Status, &item.Reason, &item.CreatedAt, &item.ReviewedAt); err != nil {
			writeError(w, 500, "unit_requests_unavailable", "Your unit requests could not be loaded")
			return
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		writeError(w, 500, "unit_requests_unavailable", "Your unit requests could not be loaded")
		return
	}
	writeJSON(w, 200, map[string]any{"requests": items})
}

func (s *server) operationsPropertyUnitRequests(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(r.Context(), `SELECT q.id,q.property_id,q.unit_code,q.unit_type,q.floor_label,q.service_scope,q.relationship,u.full_name,u.phone,l.address_line1,l.address_line2,a.locality,a.city,a.state FROM property_unit_requests q JOIN users u ON u.id=q.requester_id JOIN resident_locations l ON l.id=q.location_id JOIN service_areas a ON a.id=l.area_id WHERE q.status='pending' ORDER BY q.created_at LIMIT 300`)
	if err != nil {
		writeError(w, 500, "unit_requests_unavailable", "Unit requests could not be loaded")
		return
	}
	defer rows.Close()
	type item struct {
		ID         int64  `json:"id"`
		PropertyID int64  `json:"property_id"`
		Code       string `json:"code"`
		UnitType   string `json:"unit_type"`
		FloorLabel string `json:"floor_label"`
		Service    string `json:"service_scope"`
		Role       string `json:"relationship"`
		Resident   string `json:"resident"`
		Phone      string `json:"phone"`
		Address    string `json:"address"`
	}
	items := make([]item, 0)
	for rows.Next() {
		var x item
		var line1, line2, locality, city, state string
		if err := rows.Scan(&x.ID, &x.PropertyID, &x.Code, &x.UnitType, &x.FloorLabel, &x.Service, &x.Role, &x.Resident, &x.Phone, &line1, &line2, &locality, &city, &state); err != nil {
			writeError(w, 500, "unit_requests_unavailable", "Unit requests could not be loaded")
			return
		}
		x.Address = strings.Trim(strings.Join([]string{line1, line2, locality, city, state}, ", "), ", ")
		items = append(items, x)
	}
	if rows.Err() != nil {
		writeError(w, 500, "unit_requests_unavailable", "Unit requests could not be loaded")
		return
	}
	writeJSON(w, 200, map[string]any{"requests": items})
}

func (s *server) reviewPropertyUnitRequest(w http.ResponseWriter, r *http.Request) {
	current := r.Context().Value(userContextKey).(user)
	requestID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || requestID < 1 {
		writeError(w, 400, "invalid_unit_request", "Request id is invalid")
		return
	}
	var in struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if !oneOf(in.Decision, "approved", "rejected") {
		writeError(w, 422, "invalid_decision", "Choose approve or reject")
		return
	}
	in.Reason = strings.TrimSpace(in.Reason)
	if len(in.Reason) < 5 || len(in.Reason) > 500 {
		writeError(w, 422, "review_reason_required", "Enter a reason between 5 and 500 characters")
		return
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "unit_request_review_failed", "Unit request could not be reviewed")
		return
	}
	defer tx.Rollback(r.Context())
	var propertyID, locationID, requesterID int64
	var code, unitType, floorLabel, serviceScope, relationship string
	err = tx.QueryRow(r.Context(), `SELECT property_id,location_id,requester_id,unit_code,unit_type,floor_label,service_scope,relationship FROM property_unit_requests WHERE id=$1 AND status='pending' FOR UPDATE`, requestID).Scan(&propertyID, &locationID, &requesterID, &code, &unitType, &floorLabel, &serviceScope, &relationship)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "unit_request_not_found", "Pending unit request was not found")
		return
	}
	if err != nil {
		writeError(w, 500, "unit_request_review_failed", "Unit request could not be loaded")
		return
	}
	if in.Decision == "approved" {
		var propertyStatus, reviewStatus string
		var areaID int64
		var addressKey string
		err = tx.QueryRow(r.Context(), `SELECT status,review_status,area_id,address_key FROM registered_properties WHERE id=$1 FOR UPDATE`, propertyID).Scan(&propertyStatus, &reviewStatus, &areaID, &addressKey)
		if err != nil {
			writeError(w, 500, "unit_request_review_failed", "Property status could not be checked")
			return
		}
		if propertyStatus == "inactive" || (reviewStatus != "awaiting_bin_delivery" && reviewStatus != "verified") {
			writeError(w, 409, "property_unavailable", "Property is no longer available for unit registration")
			return
		}
		var match bool
		err = tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM resident_locations l JOIN service_areas a ON a.id=l.area_id AND a.is_active WHERE l.id=$1 AND l.resident_id=$2 AND l.deleted_at IS NULL AND a.id=$3 AND lower(btrim(l.address_line1))=$4)`, locationID, requesterID, areaID, addressKey).Scan(&match)
		if err != nil || !match {
			writeError(w, 409, "unit_address_changed", "The requested address no longer matches this property's registered address")
			return
		}
		var hadShared bool
		if err = tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM property_units WHERE property_id=$1 AND service_scope='shared_bin')`, propertyID).Scan(&hadShared); err != nil {
			writeError(w, 500, "unit_request_review_failed", "Property units could not be checked")
			return
		}
		var unitID int64
		if err = tx.QueryRow(r.Context(), `INSERT INTO property_units(property_id,unit_code,unit_type,floor_label,service_scope) VALUES($1,$2,$3,$4,$5) RETURNING id`, propertyID, code, unitType, floorLabel, serviceScope).Scan(&unitID); err != nil {
			if isUniqueViolation(err) {
				writeError(w, 409, "unit_already_exists", "That unit name is already registered in this property")
				return
			}
			writeError(w, 500, "unit_request_review_failed", "The approved unit could not be created")
			return
		}
		if _, err = tx.Exec(r.Context(), `INSERT INTO property_account_memberships(property_id,unit_id,user_id,relationship,is_primary_contact) VALUES($1,$2,$3,$4,false)`, propertyID, unitID, requesterID, relationship); err != nil {
			writeError(w, 500, "unit_request_review_failed", "Resident account could not be linked to the approved unit")
			return
		}
		needsBin := serviceScope == "individual_bin" || !hadShared
		if needsBin {
			if _, err = tx.Exec(r.Context(), `UPDATE registered_properties SET expected_bin_count=expected_bin_count+1,status='awaiting_bin_delivery',review_status='awaiting_bin_delivery',updated_at=now() WHERE id=$1`, propertyID); err != nil {
				writeError(w, 500, "unit_request_review_failed", "Property bin requirements could not be updated")
				return
			}
		}
	}
	if _, err = tx.Exec(r.Context(), `UPDATE property_unit_requests SET status=$2,reviewed_by=$3,reviewed_at=now(),review_reason=$4 WHERE id=$1`, requestID, in.Decision, current.ID, in.Reason); err != nil {
		writeError(w, 500, "unit_request_review_failed", "Review decision could not be saved")
		return
	}
	if err = s.commitAudit(r.Context(), tx, current.ID, "property_unit_request_reviewed", "property_unit_request", strconv.FormatInt(requestID, 10), map[string]any{"decision": in.Decision, "reason": in.Reason, "property_id": propertyID, "relationship": relationship}); err != nil {
		writeError(w, 500, "unit_request_review_failed", "Review decision and audit record could not be saved")
		return
	}
	writeJSON(w, 200, map[string]string{"status": in.Decision})
}

func (s *server) reviewPropertyRegistration(w http.ResponseWriter, r *http.Request) {
	current := r.Context().Value(userContextKey).(user)
	propertyID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || propertyID < 1 {
		writeError(w, 400, "invalid_property", "Property id is invalid")
		return
	}
	var in struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if !oneOf(in.Decision, "approved", "rejected") {
		writeError(w, 422, "invalid_decision", "Choose approve or reject")
		return
	}
	in.Reason = strings.TrimSpace(in.Reason)
	if len(in.Reason) < 5 || len(in.Reason) > 500 {
		writeError(w, 422, "review_reason_required", "Enter a reason between 5 and 500 characters")
		return
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "property_review_failed", "Property application could not be reviewed")
		return
	}
	defer tx.Rollback(r.Context())
	var reviewStatus string
	err = tx.QueryRow(r.Context(), `SELECT review_status FROM registered_properties WHERE id=$1 AND status<>'inactive' FOR UPDATE`, propertyID).Scan(&reviewStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "property_application_not_found", "Pending property application was not found")
		return
	}
	if err != nil {
		writeError(w, 500, "property_review_failed", "Property application could not be loaded")
		return
	}
	if reviewStatus != "pending_review" {
		writeError(w, 409, "property_application_already_reviewed", "This property application is no longer pending review")
		return
	}
	nextStatus := "rejected"
	if in.Decision == "approved" {
		nextStatus = "awaiting_bin_delivery"
	}
	if _, err = tx.Exec(r.Context(), `UPDATE registered_properties SET review_status=$2,review_reason=$3,reviewed_by=$4,reviewed_at=now(),updated_at=now() WHERE id=$1`, propertyID, nextStatus, in.Reason, current.ID); err != nil {
		writeError(w, 500, "property_review_failed", "Property application decision could not be saved")
		return
	}
	if err = s.commitAudit(r.Context(), tx, current.ID, "property_application_reviewed", "registered_property", strconv.FormatInt(propertyID, 10), map[string]any{"decision": in.Decision, "review_status": nextStatus, "reason": in.Reason}); err != nil {
		writeError(w, 500, "property_review_failed", "Decision and audit record could not be saved")
		return
	}
	writeJSON(w, 200, map[string]string{"status": nextStatus})
}
