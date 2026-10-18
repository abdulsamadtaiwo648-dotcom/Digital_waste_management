package main

import (
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
)

type residentLocation struct {
	ID                   int64    `json:"id"`
	AreaID               *int64   `json:"area_id"`
	Label                string   `json:"label"`
	AddressLine1         string   `json:"address_line1"`
	AddressLine2         string   `json:"address_line2"`
	Locality             string   `json:"locality"`
	City                 string   `json:"city"`
	State                string   `json:"state"`
	PostalCode           string   `json:"postal_code"`
	PropertyRegistered   bool     `json:"property_registered,omitempty"`
	PropertyRelationship string   `json:"property_relationship,omitempty"`
	Latitude             *float64 `json:"latitude,omitempty"`
	Longitude            *float64 `json:"longitude,omitempty"`
}

func (s *server) listLocations(w http.ResponseWriter, r *http.Request) {
	current := r.Context().Value(userContextKey).(user)
	rows, err := s.db.Query(r.Context(), `SELECT l.id,l.area_id,l.label,l.address_line1,l.address_line2,l.locality,l.city,l.state,l.postal_code,l.latitude,l.longitude,
		EXISTS(SELECT 1 FROM registered_properties p WHERE p.area_id=l.area_id AND p.address_key=lower(btrim(l.address_line1))),
		COALESCE((SELECT m.relationship FROM registered_properties p JOIN property_account_memberships m ON m.property_id=p.id AND m.user_id=l.resident_id AND m.ended_at IS NULL WHERE p.area_id=l.area_id AND p.address_key=lower(btrim(l.address_line1)) ORDER BY m.created_at LIMIT 1),'')
		FROM resident_locations l WHERE l.resident_id=$1 AND l.deleted_at IS NULL ORDER BY l.created_at,l.id`, current.ID)
	if err != nil {
		writeError(w, 500, "locations_unavailable", "Saved addresses could not be loaded")
		return
	}
	defer rows.Close()
	items := make([]residentLocation, 0)
	for rows.Next() {
		var item residentLocation
		if err := rows.Scan(&item.ID, &item.AreaID, &item.Label, &item.AddressLine1, &item.AddressLine2, &item.Locality, &item.City, &item.State, &item.PostalCode, &item.Latitude, &item.Longitude, &item.PropertyRegistered, &item.PropertyRelationship); err != nil {
			writeError(w, 500, "locations_unavailable", "Saved addresses could not be loaded")
			return
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		writeError(w, 500, "locations_unavailable", "Saved addresses could not be loaded")
		return
	}
	writeJSON(w, 200, map[string]any{"locations": items})
}

func (s *server) createLocation(w http.ResponseWriter, r *http.Request) {
	current := r.Context().Value(userContextKey).(user)
	var item residentLocation
	if !decodeJSON(w, r, &item) {
		return
	}
	if !validLocation(&item) || item.AreaID == nil || *item.AreaID < 1 {
		writeError(w, 422, "invalid_location", "Choose a service area and enter a label and street address")
		return
	}
	err := s.db.QueryRow(r.Context(), `WITH active_area AS (
		SELECT id,locality,city,state FROM service_areas WHERE id=$8 AND is_active FOR SHARE
	) INSERT INTO resident_locations(resident_id,area_id,label,address_line1,address_line2,locality,city,state,postal_code,latitude,longitude)
		SELECT $1,a.id,$2,$3,$4,a.locality,a.city,a.state,$5,$6,$7 FROM active_area a
		RETURNING id,locality,city,state`, current.ID, item.Label, item.AddressLine1, item.AddressLine2, item.PostalCode, item.Latitude, item.Longitude, *item.AreaID).Scan(&item.ID, &item.Locality, &item.City, &item.State)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 422, "service_area_unavailable", "Choose an existing active service area")
		return
	}
	if err != nil {
		writeError(w, 500, "location_save_failed", "Address could not be saved")
		return
	}
	writeJSON(w, 201, map[string]any{"location": item})
}

func (s *server) updateLocation(w http.ResponseWriter, r *http.Request) {
	current := r.Context().Value(userContextKey).(user)
	var item residentLocation
	if !decodeJSON(w, r, &item) {
		return
	}
	if !validLocation(&item) || item.AreaID == nil || *item.AreaID < 1 {
		writeError(w, 422, "invalid_location", "Choose a service area and enter a label and street address")
		return
	}
	var registered bool
	if err := s.db.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM resident_locations l JOIN registered_properties p ON p.area_id=l.area_id AND p.address_key=lower(btrim(l.address_line1)) WHERE l.id=$1)`, r.PathValue("id")).Scan(&registered); err != nil {
		writeError(w, 500, "location_save_failed", "Address registration status could not be checked")
		return
	}
	if registered {
		writeError(w, http.StatusConflict, "registered_address_locked", "This address is locked after property registration. Remove it from your active profile and add a new address if you have moved.")
		return
	}
	err := s.db.QueryRow(r.Context(), `WITH active_area AS (
		SELECT id,locality,city,state FROM service_areas WHERE id=$9 AND is_active FOR SHARE
	) UPDATE resident_locations l SET area_id=a.id,label=$1,address_line1=$2,address_line2=$3,locality=a.locality,city=a.city,state=a.state,postal_code=$4,latitude=$5,longitude=$6,updated_at=now()
		FROM active_area a WHERE l.id=$7 AND l.resident_id=$8 AND l.deleted_at IS NULL
		RETURNING l.id,l.locality,l.city,l.state`, item.Label, item.AddressLine1, item.AddressLine2, item.PostalCode, item.Latitude, item.Longitude, r.PathValue("id"), current.ID, *item.AreaID).Scan(&item.ID, &item.Locality, &item.City, &item.State)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 422, "service_area_unavailable", "Saved address or active service area was not found")
		return
	}
	if err != nil {
		writeError(w, 500, "location_save_failed", "Address could not be saved")
		return
	}
	writeJSON(w, 200, map[string]any{"location": item})
}

func (s *server) deleteLocation(w http.ResponseWriter, r *http.Request) {
	current := r.Context().Value(userContextKey).(user)
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "location_delete_failed", "Address could not be removed")
		return
	}
	defer tx.Rollback(r.Context())
	var propertyID int64
	tenantMovedOut := false
	err = tx.QueryRow(r.Context(), `SELECT p.id FROM resident_locations l JOIN registered_properties p ON p.area_id=l.area_id AND p.address_key=lower(btrim(l.address_line1)) WHERE l.id=$1 AND l.resident_id=$2 AND l.deleted_at IS NULL FOR UPDATE OF l,p`, r.PathValue("id"), current.ID).Scan(&propertyID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 500, "location_delete_failed", "Address registration could not be checked")
		return
	}
	if err == nil {
		membershipRows, membershipErr := tx.Query(r.Context(), `SELECT relationship FROM property_account_memberships WHERE property_id=$1 AND user_id=$2 AND ended_at IS NULL FOR UPDATE`, propertyID, current.ID)
		if membershipErr != nil {
			writeError(w, 500, "location_delete_failed", "Property membership could not be checked")
			return
		}
		activeMemberships := 0
		allTenantMemberships := true
		for membershipRows.Next() {
			var relationship string
			if membershipErr = membershipRows.Scan(&relationship); membershipErr != nil {
				membershipRows.Close()
				writeError(w, 500, "location_delete_failed", "Property membership could not be checked")
				return
			}
			activeMemberships++
			allTenantMemberships = allTenantMemberships && relationship == "tenant"
		}
		membershipErr = membershipRows.Err()
		membershipRows.Close()
		if membershipErr != nil {
			writeError(w, 500, "location_delete_failed", "Property membership could not be checked")
			return
		}
		if activeMemberships > 0 {
			if !allTenantMemberships {
				writeError(w, http.StatusConflict, "property_contact_required", "Only a tenant can leave this registered property from their profile. Contact management to transfer an owner or caretaker record.")
				return
			}
			ended, endErr := tx.Exec(r.Context(), `UPDATE property_account_memberships SET ended_at=now(),is_primary_contact=false WHERE property_id=$1 AND user_id=$2 AND ended_at IS NULL`, propertyID, current.ID)
			if endErr != nil {
				writeError(w, 500, "location_delete_failed", "Property membership could not be ended")
				return
			}
			tenantMovedOut = ended.RowsAffected() > 0
		}
	}
	result, err := tx.Exec(r.Context(), `UPDATE resident_locations SET deleted_at=now(),updated_at=now() WHERE id=$1 AND resident_id=$2 AND deleted_at IS NULL`, r.PathValue("id"), current.ID)
	if err != nil {
		writeError(w, 500, "location_delete_failed", "Address could not be removed")
		return
	}
	if result.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "location_not_found", "Saved address was not found")
		return
	}
	if err = s.commitAudit(r.Context(), tx, current.ID, "resident_address_removed", "resident_location", r.PathValue("id"), map[string]any{"property_id": propertyID, "tenant_moved_out": tenantMovedOut}); err != nil {
		writeError(w, 500, "location_delete_failed", "Address removal and audit record could not be saved")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func validLocation(item *residentLocation) bool {
	item.Label = strings.TrimSpace(item.Label)
	item.AddressLine1 = strings.TrimSpace(item.AddressLine1)
	item.AddressLine2 = strings.TrimSpace(item.AddressLine2)
	item.PostalCode = strings.TrimSpace(item.PostalCode)
	return len(item.Label) > 0 && len(item.Label) <= 80 && len(item.AddressLine1) > 0 && len(item.AddressLine1) <= 180 && len(item.AddressLine2) <= 180 && len(item.PostalCode) <= 24 && ((item.Latitude == nil && item.Longitude == nil) || (item.Latitude != nil && item.Longitude != nil && *item.Latitude >= -90 && *item.Latitude <= 90 && *item.Longitude >= -180 && *item.Longitude <= 180))
}

func locationText(item residentLocation) string {
	parts := []string{item.Label, item.AddressLine1, item.AddressLine2, item.Locality, item.City, item.State, item.PostalCode}
	clean := parts[:0]
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			clean = append(clean, strings.TrimSpace(part))
		}
	}
	return strings.Join(clean, ", ")
}
