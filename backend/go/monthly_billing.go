package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

type monthlyServiceInvoice struct {
	Reference    string `json:"reference"`
	LocationID   int64  `json:"location_id"`
	BillingMonth string `json:"billing_month"`
	DueDate      string `json:"due_date"`
	AmountKobo   int64  `json:"amount_kobo"`
	Status       string `json:"status"`
	CreatedAt    string `json:"created_at"`
}

// Start recurring invoice creation in the resident API process. The unique
// resident/location/month key makes repeated runs safe, including API restarts.
func (s *server) startMonthlyBilling(logger *slog.Logger) {
	go func() {
		run := func() {
			if err := s.generateMonthlyInvoices(context.Background(), time.Now()); err != nil {
				logger.Error("monthly invoice generation failed", "error", err)
			}
		}
		run()
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			run()
		}
	}()
}

func (s *server) generateMonthlyInvoices(ctx context.Context, now time.Time) error {
	lagos := time.FixedZone("WAT", 60*60)
	localNow := now.In(lagos)
	period := time.Date(localNow.Year(), localNow.Month(), 1, 0, 0, 0, 0, lagos)
	// Bills are prepared for the next month after the 7th, so a service that is
	// first deployed mid-month does not create an immediately overdue bill.
	if localNow.Day() > 7 {
		period = period.AddDate(0, 1, 0)
	}
	dueDate := period.AddDate(0, 0, 6)
	rows, err := s.db.Query(ctx, `SELECT l.id,l.resident_id FROM resident_locations l JOIN users u ON u.id=l.resident_id WHERE l.deleted_at IS NULL AND u.role='citizen' AND u.deleted_at IS NULL ORDER BY l.id`)
	if err != nil {
		return err
	}
	type residentLocation struct{ id, residentID int64 }
	locations := make([]residentLocation, 0)
	for rows.Next() {
		var location residentLocation
		if err := rows.Scan(&location.id, &location.residentID); err != nil {
			rows.Close()
			return err
		}
		locations = append(locations, location)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	for _, location := range locations {
		_, amount, breakdown, err := s.feeForLocation(ctx, s.db, location.residentID, location.id, false)
		if err != nil {
			if errors.Is(err, errLocationNotFound) || errors.Is(err, errLocationAreaUnresolved) || errors.Is(err, errServiceAreaInactive) || errors.Is(err, errAreaFeeUnconfigured) || errors.Is(err, errUnsupportedBinSize) || errors.Is(err, errBinPayerUnassigned) || errors.Is(err, errNoBillableBins) {
				continue
			}
			return err
		}
		if amount <= 0 {
			continue
		}
		snapshot, err := json.Marshal(breakdown)
		if err != nil {
			return err
		}
		ref, err := paymentReference()
		if err != nil {
			return err
		}
		_, err = s.db.Exec(ctx, `INSERT INTO monthly_service_invoices(reference,resident_id,location_id,billing_month,due_date,amount_kobo,fee_breakdown)
			VALUES($1,$2,$3,$4::date,$5::date,$6,$7::jsonb) ON CONFLICT(resident_id,location_id,billing_month) DO NOTHING`, "INV-"+ref, location.residentID, location.id, period.Format("2006-01-02"), dueDate.Format("2006-01-02"), amount, snapshot)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *server) residentMonthlyInvoices(w http.ResponseWriter, r *http.Request) {
	current := r.Context().Value(userContextKey).(user)
	var locationID int64
	if raw := r.URL.Query().Get("location_id"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 1 {
			writeError(w, http.StatusBadRequest, "invalid_location", "Choose a valid service address")
			return
		}
		locationID = parsed
		var ownerID int64
		if err := s.db.QueryRow(r.Context(), `SELECT resident_id FROM resident_locations WHERE id=$1 AND deleted_at IS NULL`, locationID).Scan(&ownerID); errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "location_not_found", "Saved service address was not found")
			return
		} else if err != nil {
			writeError(w, http.StatusInternalServerError, "invoices_unavailable", "Monthly bills could not be loaded")
			return
		} else if ownerID != current.ID {
			writeError(w, http.StatusNotFound, "location_not_found", "Saved service address was not found")
			return
		}
	}
	query := `SELECT reference,location_id,to_char(billing_month,'YYYY-MM-DD'),to_char(due_date,'YYYY-MM-DD'),amount_kobo,
		CASE WHEN status='due' AND due_date < (now() AT TIME ZONE 'Africa/Lagos')::date THEN 'overdue' ELSE status END,created_at::text
		FROM monthly_service_invoices WHERE resident_id=$1`
	args := []any{current.ID}
	if locationID > 0 {
		query += ` AND location_id=$2`
		args = append(args, locationID)
	}
	query += ` ORDER BY billing_month DESC LIMIT 24`
	rows, err := s.db.Query(r.Context(), query, args...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "invoices_unavailable", "Monthly bills could not be loaded")
		return
	}
	defer rows.Close()
	invoices := make([]monthlyServiceInvoice, 0)
	for rows.Next() {
		var invoice monthlyServiceInvoice
		if err := rows.Scan(&invoice.Reference, &invoice.LocationID, &invoice.BillingMonth, &invoice.DueDate, &invoice.AmountKobo, &invoice.Status, &invoice.CreatedAt); err != nil {
			writeError(w, http.StatusInternalServerError, "invoices_unavailable", "Monthly bills could not be loaded")
			return
		}
		invoices = append(invoices, invoice)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "invoices_unavailable", "Monthly bills could not be loaded")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"invoices": invoices})
}
