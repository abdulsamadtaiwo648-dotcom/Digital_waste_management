package main

import (
	"net/http"
	"strconv"
	"strings"
)

// assistedPropertyRegistration lets authorized field staff enter a property
// registration for an existing resident account. It never creates credentials.
func (s *server) assistedPropertyRegistration(w http.ResponseWriter, r *http.Request) {
	current := r.Context().Value(userContextKey).(user)
	var in struct {
		ResidentEmail string              `json:"resident_email"`
		AreaID        int64               `json:"area_id"`
		Label         string              `json:"label"`
		AddressLine1  string              `json:"address_line1"`
		AddressLine2  string              `json:"address_line2"`
		PostalCode    string              `json:"postal_code"`
		Type          string              `json:"property_type"`
		Structure     string              `json:"structure_type"`
		Floors        int                 `json:"floors"`
		Scope         string              `json:"registration_scope"`
		Role          string              `json:"registrant_role"`
		Units         []propertyUnitInput `json:"units"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	in.ResidentEmail = strings.TrimSpace(strings.ToLower(in.ResidentEmail))
	in.Label = strings.TrimSpace(in.Label)
	in.AddressLine1 = strings.TrimSpace(in.AddressLine1)
	in.AddressLine2 = strings.TrimSpace(in.AddressLine2)
	in.PostalCode = strings.TrimSpace(in.PostalCode)
	if !strings.Contains(in.ResidentEmail, "@") || in.AreaID < 1 || len(in.Label) < 1 || len(in.Label) > 80 || len(in.AddressLine1) < 1 || len(in.AddressLine1) > 180 || len(in.AddressLine2) > 180 || len(in.PostalCode) > 24 || !oneOf(in.Type, "residential", "commercial", "mixed_use", "other") || !oneOf(in.Structure, "bungalow", "storey_building", "apartment_block", "shop_complex", "mixed_use", "other") || in.Floors < 1 || in.Floors > 100 || !oneOf(in.Scope, "whole_property", "single_unit") || !oneOf(in.Role, "owner", "tenant", "caretaker", "agent") || len(in.Units) == 0 || len(in.Units) > 100 || (in.Scope == "single_unit" && len(in.Units) != 1) {
		writeError(w, 422, "invalid_assisted_registration", "Enter an existing resident email, active service area, valid address and property details")
		return
	}
	expectedBins := 0
	hasShared := false
	for i := range in.Units {
		u := &in.Units[i]
		u.Code = strings.TrimSpace(u.Code)
		u.FloorLabel = strings.TrimSpace(u.FloorLabel)
		if len(u.Code) == 0 || len(u.Code) > 80 || len(u.FloorLabel) > 80 || !oneOf(u.UnitType, "residential", "shop", "office", "shared", "other") || !oneOf(u.Service, "individual_bin", "shared_bin") {
			writeError(w, 422, "invalid_assisted_registration", "Each unit needs a unique name, type, floor and bin arrangement")
			return
		}
		for j := 0; j < i; j++ {
			if strings.EqualFold(in.Units[j].Code, u.Code) {
				writeError(w, 422, "duplicate_property_unit", "Unit names must be unique within the property")
				return
			}
		}
		if u.Service == "individual_bin" {
			expectedBins++
		} else {
			hasShared = true
		}
	}
	if hasShared {
		expectedBins++
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "assisted_registration_failed", "Assisted registration could not be started")
		return
	}
	defer tx.Rollback(r.Context())
	var residentID int64
	err = tx.QueryRow(r.Context(), `SELECT id FROM users WHERE lower(email)=$1 AND role='citizen' AND deleted_at IS NULL FOR SHARE`, in.ResidentEmail).Scan(&residentID)
	if err != nil {
		writeError(w, 404, "resident_not_found", "No active resident account matches that email. Confirm the account with the resident before continuing.")
		return
	}
	var locationID int64
	err = tx.QueryRow(r.Context(), `INSERT INTO resident_locations(resident_id,area_id,label,address_line1,address_line2,locality,city,state,postal_code)
		SELECT $1,a.id,$2,$3,$4,a.locality,a.city,a.state,$5 FROM service_areas a WHERE a.id=$6 AND a.is_active RETURNING id`, residentID, in.Label, in.AddressLine1, in.AddressLine2, in.PostalCode, in.AreaID).Scan(&locationID)
	if err != nil {
		writeError(w, 422, "service_area_unavailable", "Choose an existing active service area")
		return
	}
	var propertyID int64
	err = tx.QueryRow(r.Context(), `INSERT INTO registered_properties(location_id,area_id,address_key,registered_by,property_type,structure_type,floors,registration_scope,registrant_role,expected_bin_count)
		SELECT l.id,l.area_id,lower(btrim(l.address_line1)),$2,$3,$4,$5,$6,$7,$8 FROM resident_locations l WHERE l.id=$1 RETURNING id`, locationID, current.ID, in.Type, in.Structure, in.Floors, in.Scope, in.Role, expectedBins).Scan(&propertyID)
	if err != nil {
		writeError(w, 409, "property_already_registered", "This address already has a property record or could not be registered. Ask management to review it.")
		return
	}
	for _, unit := range in.Units {
		var unitID int64
		if err = tx.QueryRow(r.Context(), `INSERT INTO property_units(property_id,unit_code,unit_type,floor_label,service_scope) VALUES($1,$2,$3,$4,$5) RETURNING id`, propertyID, unit.Code, unit.UnitType, unit.FloorLabel, unit.Service).Scan(&unitID); err != nil {
			writeError(w, 500, "assisted_registration_failed", "Property units could not be registered")
			return
		}
		if _, err = tx.Exec(r.Context(), `INSERT INTO property_account_memberships(property_id,unit_id,user_id,relationship,is_primary_contact) VALUES($1,$2,$3,$4,true)`, propertyID, unitID, residentID, in.Role); err != nil {
			writeError(w, 500, "assisted_registration_failed", "Resident could not be linked to the property")
			return
		}
	}
	if err = s.commitAudit(r.Context(), tx, current.ID, "assisted_property_registration_created", "registered_property", strconv.FormatInt(propertyID, 10), map[string]any{"resident_id": residentID, "area_id": in.AreaID, "unit_count": len(in.Units), "registrant_role": in.Role}); err != nil {
		writeError(w, 500, "assisted_registration_failed", "Registration and audit record could not be saved")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]int64{"id": propertyID})
}
