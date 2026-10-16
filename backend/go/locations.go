package main

import (
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
)

type residentLocation struct {
	ID           int64    `json:"id"`
	AreaID       *int64   `json:"area_id"`
	Label        string   `json:"label"`
	AddressLine1 string   `json:"address_line1"`
	AddressLine2 string   `json:"address_line2"`
	Locality     string   `json:"locality"`
	City         string   `json:"city"`
	State        string   `json:"state"`
	PostalCode   string   `json:"postal_code"`
	Latitude     *float64 `json:"latitude,omitempty"`
	Longitude    *float64 `json:"longitude,omitempty"`
}

func (s *server) listLocations(w http.ResponseWriter, r *http.Request) {
	current := r.Context().Value(userContextKey).(user)
	rows, err := s.db.Query(r.Context(), `SELECT id,area_id,label,address_line1,address_line2,locality,city,state,postal_code,latitude,longitude FROM resident_locations WHERE resident_id=$1 AND deleted_at IS NULL ORDER BY created_at,id`, current.ID)
	if err != nil {
		writeError(w, 500, "locations_unavailable", "Saved addresses could not be loaded")
		return
	}
	defer rows.Close()
	items := make([]residentLocation, 0)
	for rows.Next() {
		var item residentLocation
		if err := rows.Scan(&item.ID, &item.AreaID, &item.Label, &item.AddressLine1, &item.AddressLine2, &item.Locality, &item.City, &item.State, &item.PostalCode, &item.Latitude, &item.Longitude); err != nil {
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
	_, err := s.db.Exec(r.Context(), `UPDATE resident_locations SET deleted_at=now(),updated_at=now() WHERE id=$1 AND resident_id=$2 AND deleted_at IS NULL`, r.PathValue("id"), current.ID)
	if err != nil {
		writeError(w, 500, "location_delete_failed", "Address could not be removed")
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
