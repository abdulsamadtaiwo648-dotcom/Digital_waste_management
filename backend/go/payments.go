package main

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

type payment struct {
	Reference     string `json:"reference"`
	LocationID    *int64 `json:"location_id,omitempty"`
	LocationLabel string `json:"location_label,omitempty"`
	ServiceCode   string `json:"service_code"`
	AmountKobo    int64  `json:"amount_kobo"`
	Status        string `json:"status"`
	Mode          string `json:"mode"`
	CreatedAt     string `json:"created_at"`
}

func (s *server) paymentOptions(w http.ResponseWriter, r *http.Request) {
	locationID := strings.TrimSpace(r.URL.Query().Get("location_id"))
	if locationID == "" {
		writeJSON(w, 200, map[string]any{"enabled": false, "simulated": s.paymentSimulation, "fee_amount_kobo": 0, "message": "Choose a saved service address to check its area fee."})
		return
	}
	id, err := strconv.ParseInt(locationID, 10, 64)
	if err != nil || id < 1 {
		writeError(w, 400, "invalid_location", "Choose a valid saved service address")
		return
	}
	current := r.Context().Value(userContextKey).(user)
	area, amount, breakdown, err := s.feeForLocation(r.Context(), s.db, current.ID, id, false)
	if errors.Is(err, errLocationNotFound) {
		writeError(w, 404, "location_not_found", "Saved service address was not found")
		return
	}
	if errors.Is(err, errLocationAreaUnresolved) {
		writeError(w, 409, "area_unresolved", "This address needs an administrator to assign its verified service area")
		return
	}
	if errors.Is(err, errServiceAreaInactive) {
		writeError(w, 409, "service_area_unavailable", "This address is assigned to an inactive service area")
		return
	}
	if err != nil && !errors.Is(err, errAreaFeeUnconfigured) {
		if errors.Is(err, errBinFeeUnconfigured) {
			areaName := area.Locality + ", " + area.City + ", " + area.State
			writeJSON(w, 200, map[string]any{"enabled": false, "simulated": s.paymentSimulation, "fee_amount_kobo": 0, "fee_breakdown": breakdown, "area_id": area.ID, "area_name": areaName, "message": "A fee has not been configured for every delivered bin size at this address. Contact service management before paying."})
			return
		}
		if errors.Is(err, errBinPayerUnassigned) {
			writeJSON(w, 200, map[string]any{"enabled": false, "simulated": s.paymentSimulation, "fee_amount_kobo": 0, "fee_breakdown": breakdown, "area_id": area.ID, "message": "A delivered bin is not linked to an active unit billing contact. Contact service management before paying."})
			return
		}
		writeError(w, 500, "payment_options_unavailable", "The area fee could not be loaded")
		return
	}
	areaName := area.Locality + ", " + area.City + ", " + area.State
	message := paymentOptionMessage(s.paymentSimulation, amount)
	if errors.Is(err, errAreaFeeUnconfigured) {
		message = "A waste fee has not been configured for this service area."
	}
	writeJSON(w, 200, map[string]any{"enabled": s.paymentSimulation && amount > 0 && err == nil, "simulated": s.paymentSimulation, "fee_amount_kobo": amount, "fee_breakdown": breakdown, "area_id": area.ID, "area_name": areaName, "message": message})
}

func paymentOptionMessage(simulated bool, amount int64) string {
	if amount <= 0 {
		return "A waste fee has not been configured."
	}
	if !simulated {
		return "Online payment is not enabled. No payment has been attempted."
	}
	return "Demonstration mode only. No real payment will be collected."
}

func (s *server) createPayment(w http.ResponseWriter, r *http.Request) {
	if !s.paymentSimulation {
		writeError(w, http.StatusServiceUnavailable, "payment_provider_unavailable", "Online payment has not been configured. No payment was attempted.")
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if len(key) < 16 || len(key) > 100 {
		writeError(w, 400, "idempotency_key_required", "A valid idempotency key is required")
		return
	}
	current := r.Context().Value(userContextKey).(user)
	var input struct {
		LocationID int64 `json:"location_id"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.LocationID < 1 {
		writeError(w, 400, "location_required", "Choose which saved home or business this service payment is for")
		return
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "payment_initialization_failed", "Payment could not be initialized")
		return
	}
	defer tx.Rollback(r.Context())
	var existing payment
	var existingLocationID int64
	err = tx.QueryRow(r.Context(), `SELECT reference,COALESCE(location_id,0),service_code,amount_kobo,status,mode,created_at::text FROM payments WHERE citizen_id=$1 AND idempotency_key=$2`, current.ID, key).Scan(&existing.Reference, &existingLocationID, &existing.ServiceCode, &existing.AmountKobo, &existing.Status, &existing.Mode, &existing.CreatedAt)
	if err == nil {
		if existingLocationID != input.LocationID {
			writeError(w, http.StatusConflict, "idempotency_key_conflict", "This idempotency key was already used for a different saved address")
			return
		}
		existing.LocationID = &existingLocationID
		writeJSON(w, 200, map[string]any{"payment": existing, "simulated": true})
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 500, "payment_initialization_failed", "Existing payment could not be checked")
		return
	}
	_, amount, breakdown, err := s.feeForLocation(r.Context(), tx, current.ID, input.LocationID, true)
	if errors.Is(err, errLocationNotFound) {
		writeError(w, 404, "location_not_found", "Saved service address was not found")
		return
	}
	if errors.Is(err, errLocationAreaUnresolved) {
		writeError(w, 409, "area_unresolved", "This address needs an administrator to assign its verified service area. No payment was initialized.")
		return
	}
	if errors.Is(err, errServiceAreaInactive) {
		writeError(w, 409, "service_area_unavailable", "This address is assigned to an inactive service area. No payment was initialized.")
		return
	}
	if errors.Is(err, errBinFeeUnconfigured) {
		writeError(w, http.StatusServiceUnavailable, "bin_fee_unconfigured", "A fee has not been configured for every delivered bin size. No payment was attempted.")
		return
	}
	if errors.Is(err, errBinPayerUnassigned) {
		writeError(w, http.StatusServiceUnavailable, "bin_payer_unassigned", "A delivered bin is not linked to an active unit billing contact. No payment was attempted.")
		return
	}
	if err != nil || amount <= 0 {
		writeError(w, http.StatusServiceUnavailable, "fee_unconfigured", "No waste fee is configured for this service address area. No payment was attempted.")
		return
	}
	reference, err := paymentReference()
	if err != nil {
		writeError(w, 500, "payment_initialization_failed", "Payment could not be initialized")
		return
	}
	var created payment
	created.LocationID = &input.LocationID
	err = tx.QueryRow(r.Context(), `INSERT INTO payments(reference,citizen_id,location_id,service_code,amount_kobo,status,mode,idempotency_key) VALUES($1,$2,$3,'waste_fee',$4,'pending','simulation',$5) ON CONFLICT(citizen_id,idempotency_key) DO NOTHING RETURNING reference,service_code,amount_kobo,status,mode,created_at::text`, reference, current.ID, input.LocationID, amount, key).Scan(&created.Reference, &created.ServiceCode, &created.AmountKobo, &created.Status, &created.Mode, &created.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		var existingLocationID int64
		err = tx.QueryRow(r.Context(), `SELECT reference,COALESCE(location_id,0),service_code,amount_kobo,status,mode,created_at::text FROM payments WHERE citizen_id=$1 AND idempotency_key=$2`, current.ID, key).Scan(&created.Reference, &existingLocationID, &created.ServiceCode, &created.AmountKobo, &created.Status, &created.Mode, &created.CreatedAt)
		if err != nil {
			writeError(w, 500, "payment_initialization_failed", "Payment could not be loaded")
			return
		}
		if existingLocationID != input.LocationID {
			writeError(w, http.StatusConflict, "idempotency_key_conflict", "This idempotency key was already used for a different saved address")
			return
		}
		created.LocationID = &existingLocationID
		writeJSON(w, 200, map[string]any{"payment": created, "simulated": true})
		return
	}
	if err != nil {
		writeError(w, 500, "payment_initialization_failed", "Payment could not be initialized")
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO payment_status_history(payment_reference,status,note) VALUES($1,'pending','Demonstration payment initialized; no charge was made.')`, created.Reference); err != nil {
		writeError(w, 500, "payment_initialization_failed", "Payment status could not be recorded")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "payment_initialization_failed", "Payment could not be initialized")
		return
	}
	writeJSON(w, 201, map[string]any{"payment": created, "fee_breakdown": breakdown, "simulated": true})
}

func (s *server) listPayments(w http.ResponseWriter, r *http.Request) {
	current := r.Context().Value(userContextKey).(user)
	rows, err := s.db.Query(r.Context(), `SELECT p.reference,p.service_code,p.amount_kobo,p.status,p.mode,p.created_at::text,r.reference,p.location_id,COALESCE(l.label,'') FROM payments p LEFT JOIN receipts r ON r.payment_reference=p.reference LEFT JOIN resident_locations l ON l.id=p.location_id WHERE p.citizen_id=$1 ORDER BY p.created_at DESC LIMIT 100`, current.ID)
	if err != nil {
		writeError(w, 500, "payments_unavailable", "Payment history could not be loaded")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var p payment
		var receipt *string
		if err := rows.Scan(&p.Reference, &p.ServiceCode, &p.AmountKobo, &p.Status, &p.Mode, &p.CreatedAt, &receipt, &p.LocationID, &p.LocationLabel); err != nil {
			writeError(w, 500, "payments_unavailable", "Payment history could not be loaded")
			return
		}
		items = append(items, map[string]any{"reference": p.Reference, "service_code": p.ServiceCode, "amount_kobo": p.AmountKobo, "status": p.Status, "mode": p.Mode, "created_at": p.CreatedAt, "receipt_reference": receipt, "location_id": p.LocationID, "location_label": p.LocationLabel})
	}
	if rows.Err() != nil {
		writeError(w, 500, "payments_unavailable", "Payment history could not be loaded")
		return
	}
	writeJSON(w, 200, map[string]any{"payments": items})
}

func (s *server) getPayment(w http.ResponseWriter, r *http.Request) {
	current := r.Context().Value(userContextKey).(user)
	var p payment
	var receipt *string
	err := s.db.QueryRow(r.Context(), `SELECT p.reference,p.service_code,p.amount_kobo,p.status,p.mode,p.created_at::text,r.reference FROM payments p LEFT JOIN receipts r ON r.payment_reference=p.reference WHERE p.reference=$1 AND p.citizen_id=$2`, r.PathValue("reference"), current.ID).Scan(&p.Reference, &p.ServiceCode, &p.AmountKobo, &p.Status, &p.Mode, &p.CreatedAt, &receipt)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "payment_not_found", "Payment was not found")
		return
	}
	if err != nil {
		writeError(w, 500, "payment_unavailable", "Payment could not be loaded")
		return
	}
	writeJSON(w, 200, map[string]any{"payment": p, "simulated": p.Mode == "simulation", "receipt_reference": receipt})
}

func (s *server) simulatePayment(w http.ResponseWriter, r *http.Request) {
	if !s.paymentSimulation {
		writeError(w, 403, "simulation_disabled", "Payment simulation is disabled")
		return
	}
	current := r.Context().Value(userContextKey).(user)
	ref := r.PathValue("reference")
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "payment_update_failed", "Payment status could not be updated")
		return
	}
	defer tx.Rollback(r.Context())
	var status, mode string
	var receipt *string
	err = tx.QueryRow(r.Context(), `SELECT status,mode,(SELECT reference FROM receipts WHERE payment_reference=payments.reference) FROM payments WHERE reference=$1 AND citizen_id=$2 FOR UPDATE`, ref, current.ID).Scan(&status, &mode, &receipt)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "payment_not_found", "Payment was not found")
		return
	}
	if err != nil {
		writeError(w, 500, "payment_update_failed", "Payment status could not be loaded")
		return
	}
	if mode != "simulation" {
		writeError(w, 403, "simulation_disabled", "This payment cannot use simulation")
		return
	}
	if status == "pending" {
		status = "succeeded"
		if _, err = tx.Exec(r.Context(), `UPDATE payments SET status='succeeded',updated_at=now() WHERE reference=$1`, ref); err != nil {
			writeError(w, 500, "payment_update_failed", "Payment status could not be updated")
			return
		}
		if _, err = tx.Exec(r.Context(), `INSERT INTO payment_status_history(payment_reference,status,note) VALUES($1,'succeeded','Marked successful in clearly labelled demonstration mode; no real funds were transferred.')`, ref); err != nil {
			writeError(w, 500, "payment_update_failed", "Payment history could not be saved")
			return
		}
		if receipt == nil {
			receipt = ptrString("RCPT-" + strings.TrimPrefix(ref, "PAY-"))
			if _, err = tx.Exec(r.Context(), `INSERT INTO receipts(reference,payment_reference) VALUES($1,$2) ON CONFLICT(payment_reference) DO NOTHING`, *receipt, ref); err != nil {
				writeError(w, 500, "receipt_creation_failed", "Demonstration receipt could not be created")
				return
			}
		}
	} else if status != "succeeded" {
		writeError(w, 409, "payment_not_pending", "Only pending demonstration payments can be completed")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "payment_update_failed", "Payment status could not be saved")
		return
	}
	writeJSON(w, 200, map[string]any{"reference": ref, "status": status, "simulated": true, "receipt_reference": receipt})
}

func (s *server) getReceipt(w http.ResponseWriter, r *http.Request) {
	current := r.Context().Value(userContextKey).(user)
	var receipt, ref, service, status, created string
	var amount int64
	var mode string
	err := s.db.QueryRow(r.Context(), `SELECT rc.reference,p.reference,p.service_code,p.status,p.created_at::text,p.amount_kobo,p.mode FROM receipts rc JOIN payments p ON p.reference=rc.payment_reference WHERE rc.reference=$1 AND p.citizen_id=$2`, r.PathValue("reference"), current.ID).Scan(&receipt, &ref, &service, &status, &created, &amount, &mode)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "receipt_not_found", "Receipt was not found")
		return
	}
	if err != nil {
		writeError(w, 500, "receipt_unavailable", "Receipt could not be loaded")
		return
	}
	writeJSON(w, 200, map[string]any{"receipt": map[string]any{"reference": receipt, "payment_reference": ref, "service_code": service, "status": status, "created_at": created, "amount_kobo": amount, "simulated": mode == "simulation"}})
}

func paymentReference() (string, error) {
	ref, err := complaintReference()
	return "PAY-" + strings.TrimPrefix(ref, "DW-"), err
}
func ptrString(value string) *string { return &value }
