package main

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	errLocationNotFound       = errors.New("resident location not found")
	errLocationAreaUnresolved = errors.New("resident location has no resolved service area")
	errServiceAreaInactive    = errors.New("service area is inactive or missing")
	errAreaFeeUnconfigured    = errors.New("service area has no configured fee")
	errBinFeeUnconfigured     = errors.New("a delivered bin has no configured size fee")
	errBinPayerUnassigned     = errors.New("a delivered bin has no active billing contact")
)

type areaRowQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

type deliveredBinFee struct {
	BinID      int64 `json:"bin_id"`
	SizeLitres int   `json:"size_litres"`
	AmountKobo int64 `json:"amount_kobo"`
}

type serviceFeeBreakdown struct {
	AreaFlatFeeKobo int64             `json:"area_flat_fee_kobo"`
	BinSizeFeeKobo  int64             `json:"bin_size_fee_kobo"`
	DeliveredBins   []deliveredBinFee `json:"delivered_bins"`
}

func (s *server) feeForLocation(ctx context.Context, q areaRowQuerier, residentID, locationID int64, lock bool) (serviceArea, int64, serviceFeeBreakdown, error) {
	lockClause := ""
	if lock {
		lockClause = " FOR SHARE"
	}
	breakdown := serviceFeeBreakdown{DeliveredBins: make([]deliveredBinFee, 0)}
	var areaID *int64
	err := q.QueryRow(ctx, `SELECT area_id FROM resident_locations WHERE id=$1 AND resident_id=$2 AND deleted_at IS NULL`+lockClause, locationID, residentID).Scan(&areaID)
	if errors.Is(err, pgx.ErrNoRows) {
		return serviceArea{}, 0, breakdown, errLocationNotFound
	}
	if err != nil {
		return serviceArea{}, 0, breakdown, err
	}
	if areaID == nil || *areaID < 1 {
		return serviceArea{}, 0, breakdown, errLocationAreaUnresolved
	}
	var area serviceArea
	err = q.QueryRow(ctx, `SELECT id,locality,city,state,is_active FROM service_areas WHERE id=$1`+lockClause, *areaID).Scan(&area.ID, &area.Locality, &area.City, &area.State, &area.Active)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !area.Active) {
		return serviceArea{}, 0, breakdown, errServiceAreaInactive
	}
	if err != nil {
		return serviceArea{}, 0, breakdown, err
	}
	var amount int64
	err = q.QueryRow(ctx, `SELECT amount_kobo FROM area_waste_fees WHERE area_id=$1`+lockClause, area.ID).Scan(&amount)
	if errors.Is(err, pgx.ErrNoRows) {
		return area, 0, breakdown, errAreaFeeUnconfigured
	}
	if err != nil {
		return serviceArea{}, 0, breakdown, err
	}
	breakdown.AreaFlatFeeKobo = amount
	total := amount
	var propertyID int64
	propertyLock := ""
	if lock {
		propertyLock = " FOR SHARE OF p"
	}
	propertyErr := q.QueryRow(ctx, `SELECT p.id FROM registered_properties p JOIN resident_locations l ON l.area_id=p.area_id AND p.address_key=lower(btrim(l.address_line1)) WHERE l.id=$1 AND p.status<>'inactive'`+propertyLock, locationID).Scan(&propertyID)
	if propertyErr != nil && !errors.Is(propertyErr, pgx.ErrNoRows) {
		return serviceArea{}, 0, breakdown, propertyErr
	}
	if propertyErr == nil {
		bins, queryErr := q.Query(ctx, `SELECT id,COALESCE(unit_id,0),size_litres,assigned_resident_id FROM property_bins WHERE property_id=$1 AND status='delivered' ORDER BY id`+lockClause, propertyID)
		if queryErr != nil {
			return serviceArea{}, 0, breakdown, queryErr
		}
		type deliveredBin struct {
			id       int64
			unit     int64
			size     int
			resident *int64
		}
		collected := make([]deliveredBin, 0)
		for bins.Next() {
			var bin deliveredBin
			if scanErr := bins.Scan(&bin.id, &bin.unit, &bin.size, &bin.resident); scanErr != nil {
				bins.Close()
				return serviceArea{}, 0, breakdown, scanErr
			}
			collected = append(collected, bin)
		}
		queryErr = bins.Err()
		bins.Close()
		if queryErr != nil {
			return serviceArea{}, 0, breakdown, queryErr
		}
		for _, bin := range collected {
			if bin.resident == nil || *bin.resident < 1 {
				return area, 0, breakdown, errBinPayerUnassigned
			}
			if *bin.resident != residentID {
				continue
			}
			var binAmount int64
			queryErr = q.QueryRow(ctx, `SELECT amount_kobo FROM area_bin_fees WHERE area_id=$1 AND size_litres=$2`+lockClause, area.ID, bin.size).Scan(&binAmount)
			if errors.Is(queryErr, pgx.ErrNoRows) {
				return area, 0, breakdown, errBinFeeUnconfigured
			}
			if queryErr != nil {
				return serviceArea{}, 0, breakdown, queryErr
			}
			breakdown.DeliveredBins = append(breakdown.DeliveredBins, deliveredBinFee{BinID: bin.id, SizeLitres: bin.size, AmountKobo: binAmount})
			breakdown.BinSizeFeeKobo += binAmount
			total += binAmount
		}
	}
	return area, total, breakdown, nil
}

type serviceArea struct {
	ID       int64  `json:"id"`
	Locality string `json:"locality"`
	City     string `json:"city"`
	State    string `json:"state"`
	Active   bool   `json:"active"`
}

func (s *server) listResidentServiceAreas(w http.ResponseWriter, r *http.Request) {
	s.listServiceAreas(w, r, true)
}

func (s *server) listOperationsServiceAreas(w http.ResponseWriter, r *http.Request) {
	s.listServiceAreas(w, r, true)
}

func (s *server) listAdminServiceAreas(w http.ResponseWriter, r *http.Request) {
	s.listServiceAreas(w, r, false)
}

func (s *server) listServiceAreas(w http.ResponseWriter, r *http.Request, activeOnly bool) {
	query := `SELECT id,locality,city,state,is_active FROM service_areas ORDER BY lower(state),lower(city),lower(locality),id`
	if activeOnly {
		query = `SELECT id,locality,city,state,is_active FROM service_areas WHERE is_active ORDER BY lower(state),lower(city),lower(locality),id`
	}
	rows, err := s.db.Query(r.Context(), query)
	if err != nil {
		writeError(w, 500, "service_areas_unavailable", "Service areas could not be loaded")
		return
	}
	defer rows.Close()
	items := make([]serviceArea, 0)
	for rows.Next() {
		var area serviceArea
		if err := rows.Scan(&area.ID, &area.Locality, &area.City, &area.State, &area.Active); err != nil {
			writeError(w, 500, "service_areas_unavailable", "Service areas could not be loaded")
			return
		}
		items = append(items, area)
	}
	if rows.Err() != nil {
		writeError(w, 500, "service_areas_unavailable", "Service areas could not be loaded")
		return
	}
	writeJSON(w, 200, map[string]any{"service_areas": items})
}

func (s *server) createServiceArea(w http.ResponseWriter, r *http.Request) {
	var area serviceArea
	if !decodeJSON(w, r, &area) {
		return
	}
	area.Locality = strings.TrimSpace(area.Locality)
	area.City = strings.TrimSpace(area.City)
	area.State = strings.TrimSpace(area.State)
	if len(area.Locality) == 0 || len(area.Locality) > 120 || len(area.City) == 0 || len(area.City) > 120 || len(area.State) == 0 || len(area.State) > 120 {
		writeError(w, 422, "invalid_service_area", "Enter a locality, city and state")
		return
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "service_area_save_failed", "Service area could not be saved")
		return
	}
	defer tx.Rollback(r.Context())
	err = tx.QueryRow(r.Context(), `INSERT INTO service_areas(locality,city,state) VALUES($1,$2,$3) RETURNING id,is_active`, area.Locality, area.City, area.State).Scan(&area.ID, &area.Active)
	if isUniqueViolation(err) {
		writeError(w, 409, "service_area_exists", "A service area with this locality, city and state already exists")
		return
	}
	if err != nil {
		writeError(w, 500, "service_area_save_failed", "Service area could not be saved")
		return
	}
	current := r.Context().Value(userContextKey).(user)
	if err := s.commitAudit(r.Context(), tx, current.ID, "service_area_created", "service_area", strconv.FormatInt(area.ID, 10), map[string]string{"locality": area.Locality, "city": area.City, "state": area.State}); err != nil {
		writeError(w, 500, "service_area_save_failed", "Service area and audit record could not be saved")
		return
	}
	writeJSON(w, 201, map[string]any{"service_area": area})
}

func (s *server) updateServiceArea(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		writeError(w, 400, "invalid_service_area", "Service area ID is invalid")
		return
	}
	var in struct {
		Active *bool `json:"active"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.Active == nil {
		writeError(w, 422, "invalid_service_area", "Specify whether the service area is active")
		return
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "service_area_save_failed", "Service area could not be updated")
		return
	}
	defer tx.Rollback(r.Context())
	var area serviceArea
	err = tx.QueryRow(r.Context(), `UPDATE service_areas SET is_active=$1,updated_at=now() WHERE id=$2 RETURNING id,locality,city,state,is_active`, *in.Active, id).Scan(&area.ID, &area.Locality, &area.City, &area.State, &area.Active)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "service_area_not_found", "Service area was not found")
		return
	}
	if err != nil {
		writeError(w, 500, "service_area_save_failed", "Service area could not be updated")
		return
	}
	current := r.Context().Value(userContextKey).(user)
	if err := s.commitAudit(r.Context(), tx, current.ID, "service_area_updated", "service_area", strconv.FormatInt(area.ID, 10), map[string]bool{"active": area.Active}); err != nil {
		writeError(w, 500, "service_area_save_failed", "Service area and audit record could not be saved")
		return
	}
	writeJSON(w, 200, map[string]any{"service_area": area})
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
