package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

func newBinTrackingID() (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return "DWMP-" + strings.ToUpper(hex.EncodeToString(random[:])), nil
}

type propertyUnitInput struct {
	Code       string `json:"code"`
	UnitType   string `json:"unit_type"`
	FloorLabel string `json:"floor_label"`
	Service    string `json:"service_scope"`
}

type propertyUnitView struct {
	ID         int64  `json:"id"`
	Code       string `json:"code"`
	UnitType   string `json:"unit_type"`
	FloorLabel string `json:"floor_label"`
	Service    string `json:"service_scope"`
}

type propertyBinView struct {
	ID               int64      `json:"id"`
	TrackingID       string     `json:"tracking_id"`
	Size             int        `json:"size_litres"`
	Status           string     `json:"status"`
	UnitCode         string     `json:"unit_code,omitempty"`
	RegisteredAt     time.Time  `json:"registered_at"`
	DeliveredAt      *time.Time `json:"delivered_at,omitempty"`
	DeliveredBy      string     `json:"delivered_by,omitempty"`
	DeliveryNote     string     `json:"delivery_note,omitempty"`
	AssignedToYou    bool       `json:"assigned_to_you,omitempty"`
	AssignedResident string     `json:"assigned_resident,omitempty"`
	LastEvacuatedAt  *time.Time `json:"last_evacuated_at,omitempty"`
	EvacuatedBy      string     `json:"evacuated_by,omitempty"`
}

type binEvacuationEventView struct {
	ID          int64     `json:"id"`
	EvacuatedAt time.Time `json:"evacuated_at"`
	StaffName   string    `json:"staff_name"`
	Note        string    `json:"note,omitempty"`
}

type registeredPropertyView struct {
	ID             int64              `json:"id"`
	LocationID     int64              `json:"location_id"`
	Address        string             `json:"address"`
	Area           string             `json:"area"`
	PropertyType   string             `json:"property_type"`
	StructureType  string             `json:"structure_type"`
	Floors         int                `json:"floors"`
	Scope          string             `json:"registration_scope"`
	RegistrantRole string             `json:"registrant_role"`
	Status         string             `json:"status"`
	ReviewStatus   string             `json:"review_status"`
	ReviewReason   string             `json:"review_reason,omitempty"`
	Units          []propertyUnitView `json:"units"`
	Bins           []propertyBinView  `json:"bins"`
}

func (s *server) registerProperty(w http.ResponseWriter, r *http.Request) {
	current := r.Context().Value(userContextKey).(user)
	var in struct {
		LocationID int64               `json:"location_id"`
		Type       string              `json:"property_type"`
		Structure  string              `json:"structure_type"`
		Floors     int                 `json:"floors"`
		Scope      string              `json:"registration_scope"`
		Role       string              `json:"registrant_role"`
		Units      []propertyUnitInput `json:"units"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.LocationID < 1 || !oneOf(in.Type, "residential", "commercial", "mixed_use", "other") || !oneOf(in.Structure, "bungalow", "storey_building", "apartment_block", "shop_complex", "mixed_use", "other") || in.Floors < 1 || in.Floors > 100 || !oneOf(in.Scope, "whole_property", "single_unit") || !oneOf(in.Role, "owner", "tenant", "caretaker", "agent") || len(in.Units) == 0 || len(in.Units) > 100 {
		writeError(w, http.StatusUnprocessableEntity, "invalid_property", "Enter the property structure, registration scope, role, saved address, and at least one unit")
		return
	}
	if in.Scope == "single_unit" && len(in.Units) != 1 {
		writeError(w, http.StatusUnprocessableEntity, "invalid_property_units", "Register exactly one unit when registering a single part")
		return
	}
	for i := range in.Units {
		u := &in.Units[i]
		u.Code = strings.TrimSpace(u.Code)
		u.FloorLabel = strings.TrimSpace(u.FloorLabel)
		if len(u.Code) == 0 || len(u.Code) > 80 || len(u.FloorLabel) > 80 || !oneOf(u.UnitType, "residential", "shop", "office", "shared", "other") || !oneOf(u.Service, "individual_bin", "shared_bin") {
			writeError(w, http.StatusUnprocessableEntity, "invalid_property_units", "Each unit needs a unique name, type, floor, and bin arrangement")
			return
		}
		for j := 0; j < i; j++ {
			if strings.EqualFold(in.Units[j].Code, u.Code) {
				writeError(w, http.StatusUnprocessableEntity, "duplicate_property_unit", "Unit names must be unique within the property")
				return
			}
		}
	}
	expectedBins := 0
	hasSharedBin := false
	for _, unit := range in.Units {
		if unit.Service == "individual_bin" {
			expectedBins++
		} else {
			hasSharedBin = true
		}
	}
	if hasSharedBin {
		expectedBins++
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "property_save_failed", "Property could not be registered")
		return
	}
	defer tx.Rollback(r.Context())
	var propertyID int64
	err = tx.QueryRow(r.Context(), `INSERT INTO registered_properties(location_id,area_id,address_key,registered_by,property_type,structure_type,floors,registration_scope,registrant_role,expected_bin_count,review_status)
		SELECT l.id,a.id,lower(btrim(l.address_line1)),$2,$3,$4,$5,$6,$7,$8,'pending_review' FROM resident_locations l JOIN service_areas a ON a.id=l.area_id AND a.is_active
		WHERE l.id=$1 AND l.resident_id=$2 AND l.deleted_at IS NULL RETURNING id`, in.LocationID, current.ID, in.Type, in.Structure, in.Floors, in.Scope, in.Role, expectedBins).Scan(&propertyID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusUnprocessableEntity, "property_address_unavailable", "Choose one of your saved addresses in an active service area")
		return
	}
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "property_already_registered", "This address may already belong to a registered property. Contact service staff to review the existing property record before registering it again.")
			return
		}
		writeError(w, http.StatusConflict, "property_already_registered", "This saved address already has a property registration, or the registration could not be saved")
		return
	}
	for _, unit := range in.Units {
		var unitID int64
		err = tx.QueryRow(r.Context(), `INSERT INTO property_units(property_id,unit_code,unit_type,floor_label,service_scope) VALUES($1,$2,$3,$4,$5) RETURNING id`, propertyID, unit.Code, unit.UnitType, unit.FloorLabel, unit.Service).Scan(&unitID)
		if err != nil {
			writeError(w, 500, "property_save_failed", "Property units could not be registered")
			return
		}
		if _, err = tx.Exec(r.Context(), `INSERT INTO property_account_memberships(property_id,unit_id,user_id,relationship,is_primary_contact) VALUES($1,$2,$3,$4,true)`, propertyID, unitID, current.ID, in.Role); err != nil {
			writeError(w, 500, "property_save_failed", "Property account could not be linked to its unit")
			return
		}
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "property_save_failed", "Property registration could not be completed")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]int64{"id": propertyID})
}

func (s *server) listProperties(w http.ResponseWriter, r *http.Request) {
	current := r.Context().Value(userContextKey).(user)
	rows, err := s.db.Query(r.Context(), `SELECT p.id,p.location_id,concat_ws(', ',l.label,l.address_line1,l.address_line2,l.locality,l.city,l.state),concat_ws(', ',a.locality,a.city,a.state),p.property_type,p.structure_type,p.floors,p.registration_scope,p.registrant_role,p.status,p.review_status,COALESCE(p.review_reason,''),u.id,u.unit_code,u.unit_type,u.floor_label,u.service_scope
		FROM property_account_memberships m JOIN registered_properties p ON p.id=m.property_id JOIN resident_locations l ON l.id=p.location_id JOIN service_areas a ON a.id=l.area_id JOIN property_units u ON u.id=m.unit_id
		WHERE m.user_id=$1 AND m.ended_at IS NULL ORDER BY p.created_at DESC,u.id`, current.ID)
	if err != nil {
		writeError(w, 500, "properties_unavailable", "Property registrations could not be loaded")
		return
	}
	defer rows.Close()
	items := make([]registeredPropertyView, 0)
	byID := map[int64]int{}
	for rows.Next() {
		var item registeredPropertyView
		var unit propertyUnitView
		if err := rows.Scan(&item.ID, &item.LocationID, &item.Address, &item.Area, &item.PropertyType, &item.StructureType, &item.Floors, &item.Scope, &item.RegistrantRole, &item.Status, &item.ReviewStatus, &item.ReviewReason, &unit.ID, &unit.Code, &unit.UnitType, &unit.FloorLabel, &unit.Service); err != nil {
			writeError(w, 500, "properties_unavailable", "Property registrations could not be loaded")
			return
		}
		idx, ok := byID[item.ID]
		if !ok {
			item.Bins = []propertyBinView{}
			item.Units = []propertyUnitView{unit}
			byID[item.ID] = len(items)
			items = append(items, item)
		} else {
			items[idx].Units = append(items[idx].Units, unit)
		}
	}
	if rows.Err() != nil {
		writeError(w, 500, "properties_unavailable", "Property registrations could not be loaded")
		return
	}
	rows.Close()
	binRows, err := s.db.Query(r.Context(), `SELECT DISTINCT ON (p.id,b.id) p.id,b.id,b.tracking_id,b.size_litres,b.status,COALESCE(u.unit_code,''),b.created_at,b.delivered_at,COALESCE(deliverer.full_name,''),COALESCE(event.note,''),COALESCE(b.assigned_resident_id=$1,false),evacuation.evacuated_at,COALESCE(evacuator.full_name,'') FROM property_account_memberships m JOIN registered_properties p ON p.id=m.property_id JOIN property_bins b ON b.property_id=p.id AND (b.unit_id IS NULL OR b.unit_id=m.unit_id) LEFT JOIN property_units u ON u.id=b.unit_id LEFT JOIN users deliverer ON deliverer.id=b.delivered_by LEFT JOIN LATERAL (SELECT note FROM bin_delivery_events WHERE bin_id=b.id ORDER BY created_at DESC,id DESC LIMIT 1) event ON true LEFT JOIN LATERAL (SELECT evacuated_at,staff_id FROM bin_evacuation_events WHERE bin_id=b.id ORDER BY evacuated_at DESC,id DESC LIMIT 1) evacuation ON true LEFT JOIN users evacuator ON evacuator.id=evacuation.staff_id WHERE m.user_id=$1 AND m.ended_at IS NULL ORDER BY p.id,b.id`, current.ID)
	if err != nil {
		writeError(w, 500, "properties_unavailable", "Property bin records could not be loaded")
		return
	}
	defer binRows.Close()
	for binRows.Next() {
		var propertyID int64
		var bin propertyBinView
		if err := binRows.Scan(&propertyID, &bin.ID, &bin.TrackingID, &bin.Size, &bin.Status, &bin.UnitCode, &bin.RegisteredAt, &bin.DeliveredAt, &bin.DeliveredBy, &bin.DeliveryNote, &bin.AssignedToYou, &bin.LastEvacuatedAt, &bin.EvacuatedBy); err != nil {
			writeError(w, 500, "properties_unavailable", "Property bin records could not be loaded")
			return
		}
		if idx, ok := byID[propertyID]; ok {
			items[idx].Bins = append(items[idx].Bins, bin)
		}
	}
	if binRows.Err() != nil {
		writeError(w, 500, "properties_unavailable", "Property bin records could not be loaded")
		return
	}
	binRows.Close()
	writeJSON(w, 200, map[string]any{"properties": items})
}

func (s *server) residentBinEvacuationHistory(w http.ResponseWriter, r *http.Request) {
	binID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || binID < 1 {
		writeError(w, http.StatusBadRequest, "invalid_bin", "Bin ID is invalid")
		return
	}
	current := r.Context().Value(userContextKey).(user)
	var visible bool
	err = s.db.QueryRow(r.Context(), `SELECT EXISTS(
		SELECT 1 FROM property_bins b
		WHERE b.id=$1 AND EXISTS (
			SELECT 1 FROM property_account_memberships m
			WHERE m.property_id=b.property_id AND m.user_id=$2 AND m.ended_at IS NULL
			AND (b.unit_id IS NULL OR b.unit_id=m.unit_id)
		)
	)`, binID, current.ID).Scan(&visible)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "bin_history_unavailable", "Bin collection history could not be loaded")
		return
	}
	if !visible {
		writeError(w, http.StatusNotFound, "bin_not_found", "Bin was not found")
		return
	}
	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, parseErr := strconv.Atoi(raw)
		if parseErr != nil || parsed < 1 || parsed > 100 {
			writeError(w, http.StatusBadRequest, "invalid_limit", "History limit must be between 1 and 100")
			return
		}
		limit = parsed
	}
	beforeID := int64(0)
	if raw := r.URL.Query().Get("before_id"); raw != "" {
		beforeID, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || beforeID < 1 {
			writeError(w, http.StatusBadRequest, "invalid_cursor", "History cursor is invalid")
			return
		}
	}
	rows, err := s.db.Query(r.Context(), `SELECT e.id,e.evacuated_at,COALESCE(staff.full_name,''),e.note
		FROM bin_evacuation_events e LEFT JOIN users staff ON staff.id=e.staff_id
		WHERE e.bin_id=$1 AND ($2=0 OR e.id<$2)
		ORDER BY e.id DESC LIMIT $3`, binID, beforeID, limit+1)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "bin_history_unavailable", "Bin collection history could not be loaded")
		return
	}
	defer rows.Close()
	events := make([]binEvacuationEventView, 0, limit+1)
	for rows.Next() {
		var event binEvacuationEventView
		if err := rows.Scan(&event.ID, &event.EvacuatedAt, &event.StaffName, &event.Note); err != nil {
			writeError(w, http.StatusInternalServerError, "bin_history_unavailable", "Bin collection history could not be loaded")
			return
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "bin_history_unavailable", "Bin collection history could not be loaded")
		return
	}
	hasMore := len(events) > limit
	if hasMore {
		events = events[:limit]
	}
	var nextBeforeID *int64
	if hasMore && len(events) > 0 {
		cursor := events[len(events)-1].ID
		nextBeforeID = &cursor
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events, "has_more": hasMore, "next_before_id": nextBeforeID})
}

func (s *server) operationsProperties(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(r.Context(), `SELECT p.id,p.status,p.review_status,COALESCE(p.review_reason,''),p.property_type,p.structure_type,p.registration_scope,p.registrant_role,l.label,l.address_line1,l.address_line2,a.locality,a.city,a.state,actor.full_name,actor.phone,unit.id,unit.unit_code,unit.service_scope
		FROM registered_properties p JOIN resident_locations l ON l.id=p.location_id JOIN service_areas a ON a.id=l.area_id
		LEFT JOIN LATERAL (SELECT m.user_id FROM property_account_memberships m WHERE m.property_id=p.id AND m.ended_at IS NULL AND m.is_primary_contact ORDER BY m.created_at LIMIT 1) contact ON true
		JOIN users actor ON actor.id=COALESCE(contact.user_id,p.registered_by) LEFT JOIN property_units unit ON unit.property_id=p.id
		WHERE p.status<>'inactive' ORDER BY p.created_at,unit.id LIMIT 1000`)
	if err != nil {
		writeError(w, 500, "properties_unavailable", "Property registrations could not be loaded")
		return
	}
	defer rows.Close()
	type item struct {
		ID           int64              `json:"id"`
		Status       string             `json:"status"`
		ReviewStatus string             `json:"review_status"`
		ReviewReason string             `json:"review_reason,omitempty"`
		PropertyType string             `json:"property_type"`
		Structure    string             `json:"structure_type"`
		Scope        string             `json:"registration_scope"`
		Role         string             `json:"registrant_role"`
		Address      string             `json:"address"`
		Resident     string             `json:"resident"`
		Phone        string             `json:"phone"`
		Units        []propertyUnitView `json:"units"`
		Bins         []propertyBinView  `json:"bins"`
	}
	items := make([]item, 0)
	byID := map[int64]int{}
	for rows.Next() {
		var x item
		var label, line1, line2, locality, city, state string
		var unitID *int64
		var unitCode *string
		var unitService *string
		if err := rows.Scan(&x.ID, &x.Status, &x.ReviewStatus, &x.ReviewReason, &x.PropertyType, &x.Structure, &x.Scope, &x.Role, &label, &line1, &line2, &locality, &city, &state, &x.Resident, &x.Phone, &unitID, &unitCode, &unitService); err != nil {
			writeError(w, 500, "properties_unavailable", "Property registrations could not be loaded")
			return
		}
		x.Address = strings.Trim(strings.Join([]string{label, line1, line2, locality, city, state}, ", "), ", ")
		idx, ok := byID[x.ID]
		if !ok {
			x.Units = []propertyUnitView{}
			x.Bins = []propertyBinView{}
			byID[x.ID] = len(items)
			items = append(items, x)
			idx = len(items) - 1
		}
		if unitID != nil && unitCode != nil && unitService != nil {
			items[idx].Units = append(items[idx].Units, propertyUnitView{ID: *unitID, Code: *unitCode, Service: *unitService})
		}
	}
	if rows.Err() != nil {
		writeError(w, 500, "properties_unavailable", "Property registrations could not be loaded")
		return
	}
	binRows, err := s.db.Query(r.Context(), `SELECT b.property_id,b.id,b.tracking_id,b.size_litres,b.status,COALESCE(u.unit_code,''),b.created_at,b.delivered_at,COALESCE(deliverer.full_name,''),COALESCE(event.note,''),COALESCE(assigned.full_name,'Unassigned'),evacuation.evacuated_at,COALESCE(evacuator.full_name,'') FROM property_bins b LEFT JOIN property_units u ON u.id=b.unit_id LEFT JOIN users deliverer ON deliverer.id=b.delivered_by LEFT JOIN users assigned ON assigned.id=b.assigned_resident_id LEFT JOIN LATERAL (SELECT note FROM bin_delivery_events WHERE bin_id=b.id ORDER BY created_at DESC,id DESC LIMIT 1) event ON true LEFT JOIN LATERAL (SELECT evacuated_at,staff_id FROM bin_evacuation_events WHERE bin_id=b.id ORDER BY evacuated_at DESC,id DESC LIMIT 1) evacuation ON true LEFT JOIN users evacuator ON evacuator.id=evacuation.staff_id JOIN registered_properties p ON p.id=b.property_id WHERE p.status<>'inactive' ORDER BY b.property_id,b.id`)
	if err != nil {
		writeError(w, 500, "properties_unavailable", "Property bin records could not be loaded")
		return
	}
	defer binRows.Close()
	for binRows.Next() {
		var propertyID int64
		var bin propertyBinView
		if err := binRows.Scan(&propertyID, &bin.ID, &bin.TrackingID, &bin.Size, &bin.Status, &bin.UnitCode, &bin.RegisteredAt, &bin.DeliveredAt, &bin.DeliveredBy, &bin.DeliveryNote, &bin.AssignedResident, &bin.LastEvacuatedAt, &bin.EvacuatedBy); err != nil {
			writeError(w, 500, "properties_unavailable", "Property bin records could not be loaded")
			return
		}
		if idx, ok := byID[propertyID]; ok {
			items[idx].Bins = append(items[idx].Bins, bin)
		}
	}
	if binRows.Err() != nil {
		writeError(w, 500, "properties_unavailable", "Property bin records could not be loaded")
		return
	}
	writeJSON(w, 200, map[string]any{"properties": items})
}

func (s *server) createPropertyBin(w http.ResponseWriter, r *http.Request) {
	current := r.Context().Value(userContextKey).(user)
	propertyID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || propertyID < 1 {
		writeError(w, 400, "invalid_property", "Property id is invalid")
		return
	}
	var in struct {
		UnitID        *int64 `json:"unit_id"`
		ResidentEmail string `json:"resident_email"`
		Size          int    `json:"size_litres"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	in.ResidentEmail = strings.TrimSpace(strings.ToLower(in.ResidentEmail))
	if len(in.ResidentEmail) > 254 || in.Size < 1 {
		writeError(w, 422, "invalid_bin", "Choose a resident account and enter a bin size in litres")
		return
	}
	trackingID, err := newBinTrackingID()
	if err != nil {
		writeError(w, 500, "bin_save_failed", "A bin tracking ID could not be generated")
		return
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "bin_save_failed", "Bin could not be registered")
		return
	}
	defer tx.Rollback(r.Context())
	var expected, registered int
	err = tx.QueryRow(r.Context(), `SELECT expected_bin_count FROM registered_properties WHERE id=$1 AND status IN ('awaiting_bin_delivery','needs_review') AND review_status='awaiting_bin_delivery' FOR UPDATE`, propertyID).Scan(&expected)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "property_not_found", "Property was not found or no longer needs a bin")
		return
	}
	if err != nil {
		writeError(w, 500, "bin_save_failed", "Property bin requirements could not be loaded")
		return
	}
	if err = tx.QueryRow(r.Context(), `SELECT count(*) FROM property_bins WHERE property_id=$1 AND status IN ('awaiting_delivery','delivered')`, propertyID).Scan(&registered); err != nil {
		writeError(w, 500, "bin_save_failed", "Registered bin count could not be loaded")
		return
	}
	if registered >= expected {
		writeError(w, 409, "bin_count_reached", "The required number of bins has already been registered")
		return
	}
	var residentID int64
	if in.ResidentEmail != "" && in.UnitID == nil {
		err = tx.QueryRow(r.Context(), `SELECT u.id FROM users u JOIN property_account_memberships m ON m.user_id=u.id WHERE lower(u.email)=$1 AND u.role='citizen' AND u.deleted_at IS NULL AND m.property_id=$2 AND m.ended_at IS NULL AND m.is_primary_contact ORDER BY m.created_at LIMIT 1 FOR SHARE OF m,u`, in.ResidentEmail, propertyID).Scan(&residentID)
	} else if in.ResidentEmail != "" {
		err = tx.QueryRow(r.Context(), `SELECT u.id FROM users u JOIN property_account_memberships m ON m.user_id=u.id WHERE lower(u.email)=$1 AND u.role='citizen' AND u.deleted_at IS NULL AND m.property_id=$2 AND m.unit_id=$3 AND m.ended_at IS NULL FOR SHARE OF m,u`, in.ResidentEmail, propertyID, *in.UnitID).Scan(&residentID)
	} else if in.UnitID == nil {
		err = tx.QueryRow(r.Context(), `SELECT m.user_id FROM property_account_memberships m JOIN users u ON u.id=m.user_id AND u.role='citizen' AND u.deleted_at IS NULL WHERE m.property_id=$1 AND m.ended_at IS NULL AND m.is_primary_contact ORDER BY m.created_at,m.user_id LIMIT 1 FOR SHARE OF m,u`, propertyID).Scan(&residentID)
	} else {
		err = tx.QueryRow(r.Context(), `SELECT m.user_id FROM property_account_memberships m JOIN users u ON u.id=m.user_id AND u.role='citizen' AND u.deleted_at IS NULL WHERE m.property_id=$1 AND m.unit_id=$2 AND m.ended_at IS NULL ORDER BY (m.relationship='tenant') DESC,m.is_primary_contact DESC,m.created_at,m.user_id LIMIT 1 FOR SHARE OF m,u`, propertyID, *in.UnitID).Scan(&residentID)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 409, "bin_account_unassigned", "Choose an active resident account linked to this bin's unit or property contact")
		return
	}
	if err != nil {
		writeError(w, 500, "bin_save_failed", "Bin's resident account could not be determined")
		return
	}
	var binID int64
	err = tx.QueryRow(r.Context(), `INSERT INTO property_bins(property_id,unit_id,assigned_resident_id,tracking_id,size_litres,created_by) SELECT p.id,$2,$3,$4,$5,$6 FROM registered_properties p WHERE p.id=$1 AND p.status IN ('awaiting_bin_delivery','needs_review') AND p.review_status='awaiting_bin_delivery' AND (($2::bigint IS NULL AND EXISTS(SELECT 1 FROM property_units u WHERE u.property_id=p.id AND u.service_scope='shared_bin')) OR ($2::bigint IS NOT NULL AND EXISTS(SELECT 1 FROM property_units u WHERE u.id=$2 AND u.property_id=p.id AND u.service_scope='individual_bin'))) RETURNING id`, propertyID, in.UnitID, residentID, trackingID, in.Size, current.ID).Scan(&binID)
	if err != nil {
		writeError(w, 422, "property_or_bin_unavailable", "Property or unit was not found, or the bin could not be assigned")
		return
	}
	if err = s.commitAudit(r.Context(), tx, current.ID, "property_bin_registered", "property_bin", strconv.FormatInt(binID, 10), map[string]any{"property_id": propertyID, "unit_id": in.UnitID, "size_litres": in.Size}); err != nil {
		writeError(w, 500, "bin_save_failed", "Bin registration could not be recorded")
		return
	}
	writeJSON(w, 201, map[string]any{"id": binID, "tracking_id": trackingID})
}

func (s *server) recordBinDelivery(w http.ResponseWriter, r *http.Request) {
	current := r.Context().Value(userContextKey).(user)
	binID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || binID < 1 {
		writeError(w, 400, "invalid_bin", "Bin id is invalid")
		return
	}
	var in struct {
		Outcome string `json:"outcome"`
		Note    string `json:"note"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Note = strings.TrimSpace(in.Note)
	if !oneOf(in.Outcome, "delivered", "failed") || len(in.Note) > 500 {
		writeError(w, 422, "invalid_delivery", "Choose delivered or failed and keep the note under 500 characters")
		return
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "delivery_save_failed", "Delivery result could not be recorded")
		return
	}
	defer tx.Rollback(r.Context())
	// Keep lock order consistent with fee calculation and bin registration:
	// lock the property before its bin to avoid a property/bin deadlock.
	var propertyID int64
	err = tx.QueryRow(r.Context(), `SELECT property_id FROM property_bins WHERE id=$1`, binID).Scan(&propertyID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "bin_not_found", "Bin was not found")
		return
	}
	if err != nil {
		writeError(w, 500, "delivery_save_failed", "Bin could not be loaded")
		return
	}
	var lockedPropertyID int64
	err = tx.QueryRow(r.Context(), `SELECT id FROM registered_properties WHERE id=$1 FOR UPDATE`, propertyID).Scan(&lockedPropertyID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "bin_not_found", "Bin was not found")
		return
	}
	if err != nil {
		writeError(w, 500, "delivery_save_failed", "Property could not be locked for delivery")
		return
	}
	var status string
	err = tx.QueryRow(r.Context(), `SELECT status FROM property_bins WHERE id=$1 AND property_id=$2 FOR UPDATE`, binID, propertyID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "bin_not_found", "Bin was not found")
		return
	}
	if err != nil {
		writeError(w, 500, "delivery_save_failed", "Bin could not be locked for delivery")
		return
	}
	if status != "awaiting_delivery" {
		writeError(w, 409, "bin_already_processed", "This bin is no longer awaiting delivery")
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO bin_delivery_events(bin_id,staff_id,outcome,note) VALUES($1,$2,$3,$4)`, binID, current.ID, in.Outcome, in.Note); err != nil {
		writeError(w, 500, "delivery_save_failed", "Delivery result could not be saved")
		return
	}
	if in.Outcome == "delivered" {
		if _, err = tx.Exec(r.Context(), `UPDATE property_bins SET status='delivered',delivered_at=now(),delivered_by=$2 WHERE id=$1`, binID, current.ID); err != nil {
			writeError(w, 500, "delivery_save_failed", "Bin delivery could not be completed")
			return
		}
		if _, err = tx.Exec(r.Context(), `UPDATE registered_properties SET status='verified_by_delivery',review_status='verified',updated_at=now() WHERE id=$1 AND (SELECT count(*) FROM property_bins b WHERE b.property_id=$1 AND b.status='delivered')>=expected_bin_count`, propertyID); err != nil {
			writeError(w, 500, "delivery_save_failed", "Property verification could not be updated")
			return
		}
	} else {
		if _, err = tx.Exec(r.Context(), `UPDATE property_bins SET status='failed' WHERE id=$1`, binID); err != nil {
			writeError(w, 500, "delivery_save_failed", "Failed delivery attempt could not be saved")
			return
		}
	}
	if err = s.commitAudit(r.Context(), tx, current.ID, "bin_delivery_recorded", "property_bin", strconv.FormatInt(binID, 10), map[string]any{"outcome": in.Outcome, "property_id": propertyID}); err != nil {
		writeError(w, 500, "delivery_save_failed", "Delivery audit record could not be saved")
		return
	}
	writeJSON(w, 200, map[string]string{"status": in.Outcome})
}

func (s *server) recordBinEvacuation(w http.ResponseWriter, r *http.Request) {
	current := r.Context().Value(userContextKey).(user)
	var in struct {
		TrackingID    string `json:"tracking_id"`
		ResidentEmail string `json:"resident_email"`
		Note          string `json:"note"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	in.TrackingID = strings.TrimSpace(in.TrackingID)
	in.ResidentEmail = strings.TrimSpace(strings.ToLower(in.ResidentEmail))
	in.Note = strings.TrimSpace(in.Note)
	if len(in.TrackingID) < 4 || len(in.TrackingID) > 80 || len(in.ResidentEmail) > 254 || len(in.Note) > 500 {
		writeError(w, 422, "invalid_bin_scan", "Scan a valid printed bin ID and keep the optional note under 500 characters")
		return
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "bin_evacuation_failed", "Bin evacuation could not be recorded")
		return
	}
	defer tx.Rollback(r.Context())
	var propertyID int64
	err = tx.QueryRow(r.Context(), `SELECT property_id FROM property_bins WHERE tracking_id=$1`, in.TrackingID).Scan(&propertyID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "bin_not_found", "No bin matches this tracking ID")
		return
	}
	if err != nil {
		writeError(w, 500, "bin_evacuation_failed", "Bin record could not be loaded")
		return
	}
	var activeProperty bool
	err = tx.QueryRow(r.Context(), `SELECT status<>'inactive' FROM registered_properties WHERE id=$1 FOR SHARE`, propertyID).Scan(&activeProperty)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !activeProperty) {
		writeError(w, 409, "property_inactive", "This bin belongs to an inactive property")
		return
	}
	if err != nil {
		writeError(w, 500, "bin_evacuation_failed", "Property status could not be checked")
		return
	}
	var binID int64
	var unitCode, residentName, residentEmail, status string
	var residentID *int64
	var unitID *int64
	err = tx.QueryRow(r.Context(), `SELECT b.id,b.status,b.assigned_resident_id,b.unit_id,COALESCE(unit.unit_code,''),COALESCE(resident.full_name,''),COALESCE(resident.email,'') FROM property_bins b LEFT JOIN property_units unit ON unit.id=b.unit_id LEFT JOIN users resident ON resident.id=b.assigned_resident_id WHERE b.property_id=$1 AND b.tracking_id=$2 FOR UPDATE OF b`, propertyID, in.TrackingID).Scan(&binID, &status, &residentID, &unitID, &unitCode, &residentName, &residentEmail)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "bin_not_found", "No bin matches this tracking ID")
		return
	}
	if err != nil {
		writeError(w, 500, "bin_evacuation_failed", "Bin record could not be locked")
		return
	}
	if status != "delivered" {
		writeError(w, 409, "bin_not_delivered", "Only a delivered bin can be recorded as evacuated")
		return
	}
	assignedDuringScan := false
	if residentID == nil || *residentID < 1 {
		if in.ResidentEmail == "" {
			writeError(w, 409, "bin_account_unassigned", "This older bin has no linked resident account. Confirm the correct resident email to assign it while recording this scan.")
			return
		}
		var candidateID int64
		err = tx.QueryRow(r.Context(), `SELECT u.id FROM users u JOIN property_account_memberships m ON m.user_id=u.id WHERE lower(u.email)=$1 AND u.role='citizen' AND u.deleted_at IS NULL AND m.property_id=$2 AND m.ended_at IS NULL AND (($3::bigint IS NOT NULL AND m.unit_id=$3) OR ($3::bigint IS NULL AND m.is_primary_contact)) ORDER BY m.created_at LIMIT 1 FOR SHARE OF m,u`, in.ResidentEmail, propertyID, unitID).Scan(&candidateID)
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, 422, "bin_account_mismatch", "That resident does not have active access to this bin's unit or property contact role")
			return
		}
		if err != nil {
			writeError(w, 500, "bin_evacuation_failed", "Resident account could not be checked")
			return
		}
		if _, err = tx.Exec(r.Context(), `UPDATE property_bins SET assigned_resident_id=$2 WHERE id=$1 AND assigned_resident_id IS NULL`, binID, candidateID); err != nil {
			writeError(w, 500, "bin_evacuation_failed", "Bin account assignment could not be saved")
			return
		}
		residentID = &candidateID
		if err = tx.QueryRow(r.Context(), `SELECT full_name,email FROM users WHERE id=$1`, candidateID).Scan(&residentName, &residentEmail); err != nil {
			writeError(w, 500, "bin_evacuation_failed", "Assigned resident account could not be loaded")
			return
		}
		assignedDuringScan = true
	}
	if residentName == "" {
		writeError(w, 409, "bin_account_unassigned", "This bin's resident account is inactive or unavailable")
		return
	}
	var eventID int64
	var evacuatedAt time.Time
	err = tx.QueryRow(r.Context(), `INSERT INTO bin_evacuation_events(bin_id,staff_id,note) VALUES($1,$2,$3) RETURNING id,evacuated_at`, binID, current.ID, in.Note).Scan(&eventID, &evacuatedAt)
	if err != nil {
		writeError(w, 500, "bin_evacuation_failed", "Evacuation event could not be saved")
		return
	}
	if err = s.writeAudit(r.Context(), tx, current.ID, "bin_evacuated", "property_bin", strconv.FormatInt(binID, 10), map[string]any{"event_id": eventID, "resident_id": *residentID, "unit_id": unitID, "tracking_id": in.TrackingID, "account_assigned_during_scan": assignedDuringScan}); err != nil {
		writeError(w, 500, "bin_evacuation_failed", "Evacuation and audit records could not be saved")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "bin_evacuation_failed", "Evacuation could not be recorded")
		return
	}
	writeJSON(w, 201, map[string]any{"event_id": eventID, "tracking_id": in.TrackingID, "status": "evacuated", "evacuated_at": evacuatedAt, "resident": map[string]string{"full_name": residentName, "email": residentEmail}, "unit_code": unitCode})
}
