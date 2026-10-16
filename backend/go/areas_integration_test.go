package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestManagedAreaMigrationAndAPIs(t *testing.T) {
	dsn := os.Getenv("DWMP_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set DWMP_TEST_DATABASE_URL to an isolated PostgreSQL database whose name contains 'test'")
	}

	pool, cleanup := managedAreaTestDatabase(t, dsn)
	defer cleanup()
	s := &server{db: pool, paymentSimulation: true}
	ctx := context.Background()
	var residentID int64
	if err := pool.QueryRow(ctx, `INSERT INTO users(full_name,email,phone,password_hash,role) VALUES('Area Test Resident','area-test@example.test','+234000000000','test-hash','citizen') RETURNING id`).Scan(&residentID); err != nil {
		t.Fatal(err)
	}

	locationA := insertLegacyLocation(t, pool, residentID, "Springfield", "Metro A", "North State")
	locationB := insertLegacyLocation(t, pool, residentID, "Springfield", "Metro B", "South State")
	uniqueLocation := insertLegacyLocation(t, pool, residentID, "Harbor", "Port City", "Coastal State")
	_, err := pool.Exec(ctx, `INSERT INTO area_waste_fees(area_name,amount_kobo) VALUES('Springfield',51000),('Harbor',7200),('No Address',8300)`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO payments(reference,citizen_id,location_id,service_code,amount_kobo,status,mode,idempotency_key)
		VALUES('HISTORIC-AREA-001',$1,$2,'waste_fee',123456,'pending','simulation','historical-area-key-001')`, residentID, locationA)
	if err != nil {
		t.Fatal(err)
	}

	if err := applyManagedAreaMigration(t, pool); err != nil {
		t.Fatal(err)
	}
	if err := applyManagedAreaMigration(t, pool); err != nil {
		t.Fatalf("additive migration was not safe to reapply: %v", err)
	}
	var mappedHarbor, unresolvedSpringfield, unresolvedNoAddress *int64
	var springfieldFeeID int64
	if err := pool.QueryRow(ctx, `SELECT area_id FROM area_waste_fees WHERE lower(area_name)='harbor'`).Scan(&mappedHarbor); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT id,area_id FROM area_waste_fees WHERE lower(area_name)='springfield'`).Scan(&springfieldFeeID, &unresolvedSpringfield); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT area_id FROM area_waste_fees WHERE lower(area_name)='no address'`).Scan(&unresolvedNoAddress); err != nil {
		t.Fatal(err)
	}
	if mappedHarbor == nil || unresolvedSpringfield != nil || unresolvedNoAddress != nil {
		t.Fatalf("incorrect legacy fee mapping: Harbor=%v Springfield=%v unmatched=%v", mappedHarbor, unresolvedSpringfield, unresolvedNoAddress)
	}
	var harborLocationArea *int64
	if err := pool.QueryRow(ctx, `SELECT area_id FROM resident_locations WHERE id=$1`, uniqueLocation).Scan(&harborLocationArea); err != nil || harborLocationArea == nil || *harborLocationArea != *mappedHarbor {
		t.Fatalf("unambiguous Harbor address did not map to its service area: location=%v fee=%v err=%v", harborLocationArea, mappedHarbor, err)
	}
	var historicalAmount int64
	if err := pool.QueryRow(ctx, `SELECT amount_kobo FROM payments WHERE reference='HISTORIC-AREA-001'`).Scan(&historicalAmount); err != nil || historicalAmount != 123456 {
		t.Fatalf("historical payment amount changed: amount=%d err=%v", historicalAmount, err)
	}
	var migratedA, migratedB *int64
	if err := pool.QueryRow(ctx, `SELECT area_id FROM resident_locations WHERE id=$1`, locationA).Scan(&migratedA); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT area_id FROM resident_locations WHERE id=$1`, locationB).Scan(&migratedB); err != nil {
		t.Fatal(err)
	}
	if migratedA == nil || migratedB == nil || *migratedA == *migratedB {
		t.Fatalf("same-named localities in separate cities were not kept distinct: %v %v", migratedA, migratedB)
	}

	admin := user{ID: residentID, Role: "system_administrator"}
	resident := user{ID: residentID, Role: "citizen"}
	areaA := createArea(t, s, admin, "Oak Park", "Metro A", "North State")
	areaB := createArea(t, s, admin, "Oak Park", "Metro B", "South State")
	if areaA.ID == areaB.ID {
		t.Fatal("same locality in different cities returned the same area ID")
	}
	if saveFee(t, s, admin, areaA.ID, 1450, 0) != http.StatusOK || saveFee(t, s, admin, areaB.ID, 2890, 0) != http.StatusOK {
		t.Fatal("administrator could not create fees for both distinct areas")
	}
	duplicateAreaBody := `{"locality":"Oak Park","city":"Metro A","state":"North State"}`
	if status := callAs(s.createServiceArea, http.MethodPost, "/api/v1/admin/service-areas", duplicateAreaBody, admin).Code; status != http.StatusConflict {
		t.Fatalf("duplicate geographic identity returned %d, want 409", status)
	}
	addressA := createResidentLocation(t, s, resident, areaA.ID)
	addressB := createResidentLocation(t, s, resident, areaB.ID)
	assertOptionFee(t, s, resident, addressA, areaA.ID, 1450)
	assertOptionFee(t, s, resident, addressB, areaB.ID, 2890)

	if saveFee(t, s, admin, areaA.ID, 1575, 0) != http.StatusOK {
		t.Fatal("administrator could not update an area's fee")
	}
	assertOptionFee(t, s, resident, addressA, areaA.ID, 1575)
	if status := saveFee(t, s, admin, 99999999, 1000, 0); status != http.StatusUnprocessableEntity {
		t.Fatalf("fee creation for nonexistent area returned %d, want 422", status)
	}
	editAddressBody := fmt.Sprintf(`{"area_id":%d,"label":"Test home","address_line1":"22 Sample Road","address_line2":"","postal_code":""}`, areaB.ID)
	editAddress := callAs(s.updateLocation, http.MethodPatch, fmt.Sprintf("/api/v1/locations/%d", addressA), editAddressBody, resident)
	if editAddress.Code != http.StatusOK {
		t.Fatalf("resident address area update returned %d: %s", editAddress.Code, editAddress.Body.String())
	}
	assertOptionFee(t, s, resident, addressA, areaB.ID, 2890)
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM area_waste_fees WHERE area_id=$1`, areaA.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("duplicate fee records created for one area: count=%d err=%v", count, err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO area_waste_fees(area_id,area_name,amount_kobo) VALUES($1,'Oak Park',999)`, areaA.ID); err == nil {
		t.Fatal("database unique index allowed a second fee for the same service area")
	}

	if status := callLocationCreate(t, s, resident, map[string]any{"label": "Missing Area", "address_line1": "1 Test Road"}); status != http.StatusUnprocessableEntity {
		t.Fatalf("address without area_id returned %d, want 422", status)
	}
	if status := callLocationCreate(t, s, resident, map[string]any{"area_id": 99999999, "label": "Invalid Area", "address_line1": "1 Test Road"}); status != http.StatusUnprocessableEntity {
		t.Fatalf("address with nonexistent area returned %d, want 422", status)
	}
	if status := paymentOptionStatus(t, s, resident, locationA); status != http.StatusOK {
		t.Fatalf("resolved address without configured unambiguous fee returned %d, want 200", status)
	}
	missingFeeReq := httptest.NewRequest(http.MethodPost, "/api/v1/payments", strings.NewReader(fmt.Sprintf(`{"location_id":%d}`, locationA)))
	missingFeeReq.Header.Set("Idempotency-Key", "missing-area-fee-payment-001")
	missingFeeReq = missingFeeReq.WithContext(context.WithValue(missingFeeReq.Context(), userContextKey, resident))
	missingFeeResponse := httptest.NewRecorder()
	s.createPayment(missingFeeResponse, missingFeeReq)
	if missingFeeResponse.Code != http.StatusServiceUnavailable {
		t.Fatalf("payment with unresolved legacy fee returned %d, want 503", missingFeeResponse.Code)
	}
	if _, err := pool.Exec(ctx, `UPDATE resident_locations SET area_id=NULL WHERE id=$1`, locationA); err != nil {
		t.Fatal(err)
	}
	if status := paymentOptionStatus(t, s, resident, locationA); status != http.StatusConflict {
		t.Fatalf("address with missing area assignment returned %d, want 409", status)
	}
	unresolvedPaymentReq := httptest.NewRequest(http.MethodPost, "/api/v1/payments", strings.NewReader(fmt.Sprintf(`{"location_id":%d}`, locationA)))
	unresolvedPaymentReq.Header.Set("Idempotency-Key", "unresolved-area-payment-001")
	unresolvedPaymentReq = unresolvedPaymentReq.WithContext(context.WithValue(unresolvedPaymentReq.Context(), userContextKey, resident))
	unresolvedPaymentResponse := httptest.NewRecorder()
	s.createPayment(unresolvedPaymentResponse, unresolvedPaymentReq)
	if unresolvedPaymentResponse.Code != http.StatusConflict {
		t.Fatalf("unresolved address payment create returned %d, want 409", unresolvedPaymentResponse.Code)
	}
	if status := saveFee(t, s, admin, areaA.ID, 51000, springfieldFeeID); status != http.StatusConflict {
		t.Fatalf("duplicate legacy reconciliation returned %d, want 409", status)
	}
	var stillUnresolved *int64
	var legacyAmount int64
	if err := pool.QueryRow(ctx, `SELECT area_id,amount_kobo FROM area_waste_fees WHERE lower(area_name)='springfield'`).Scan(&stillUnresolved, &legacyAmount); err != nil || stillUnresolved != nil || legacyAmount != 51000 {
		t.Fatalf("duplicate reconciliation did not preserve unresolved legacy fee: area=%v amount=%d err=%v", stillUnresolved, legacyAmount, err)
	}

	inactive := createArea(t, s, admin, "Old Quarter", "Old City", "North State")
	if saveFee(t, s, admin, inactive.ID, 1200, 0) != http.StatusOK {
		t.Fatal("could not save inactive-area test fee")
	}
	inactiveLocation := createResidentLocation(t, s, resident, inactive.ID)
	deactivateArea(t, s, admin, inactive.ID)
	if status := paymentOptionStatus(t, s, resident, inactiveLocation); status != http.StatusConflict {
		t.Fatalf("inactive area payment options returned %d, want 409", status)
	}

	key := "test-idempotency-area-001"
	req := httptest.NewRequest(http.MethodPost, "/api/v1/payments", strings.NewReader(fmt.Sprintf(`{"location_id":%d}`, addressB)))
	req.Header.Set("Idempotency-Key", key)
	req = req.WithContext(context.WithValue(req.Context(), userContextKey, resident))
	response := httptest.NewRecorder()
	s.createPayment(response, req)
	if response.Code != http.StatusCreated {
		t.Fatalf("resident create payment returned %d: %s", response.Code, response.Body.String())
	}
	var paymentPayload struct {
		Payment payment `json:"payment"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &paymentPayload); err != nil || paymentPayload.Payment.AmountKobo != 2890 {
		t.Fatalf("payment used wrong area fee: %+v err=%v", paymentPayload.Payment, err)
	}
	replayReq := httptest.NewRequest(http.MethodPost, "/api/v1/payments", strings.NewReader(fmt.Sprintf(`{"location_id":%d}`, addressB)))
	replayReq.Header.Set("Idempotency-Key", key)
	replayReq = replayReq.WithContext(context.WithValue(replayReq.Context(), userContextKey, resident))
	replayResponse := httptest.NewRecorder()
	s.createPayment(replayResponse, replayReq)
	var replayPayload struct {
		Payment payment `json:"payment"`
	}
	if replayResponse.Code != http.StatusOK || json.Unmarshal(replayResponse.Body.Bytes(), &replayPayload) != nil || replayPayload.Payment.Reference != paymentPayload.Payment.Reference || replayPayload.Payment.AmountKobo != paymentPayload.Payment.AmountKobo {
		t.Fatalf("same-key replay did not return the existing payment: status=%d payment=%+v body=%s", replayResponse.Code, replayPayload.Payment, replayResponse.Body.String())
	}
}

func managedAreaTestDatabase(t *testing.T, dsn string) (*pgxpool.Pool, func()) {
	t.Helper()
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse DWMP_TEST_DATABASE_URL: %v", err)
	}
	if !strings.Contains(strings.ToLower(config.Database), "test") {
		t.Fatalf("refusing integration tests: database name %q must contain 'test'", config.Database)
	}
	schema := fmt.Sprintf("dwmp_area_test_%d", time.Now().UnixNano())
	adminConn, err := pgx.ConnectConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("connect to explicit test database: %v", err)
	}
	if _, err := adminConn.Exec(context.Background(), `CREATE SCHEMA `+schema); err != nil {
		adminConn.Close(context.Background())
		t.Fatalf("create isolated integration schema: %v", err)
	}
	cleanup := func() {
		_, _ = adminConn.Exec(context.Background(), `DROP SCHEMA `+schema+` CASCADE`)
		_ = adminConn.Close(context.Background())
	}
	poolConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		cleanup()
		t.Fatal(err)
	}
	poolConfig.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(context.Background(), poolConfig)
	if err != nil {
		cleanup()
		t.Fatalf("open isolated test pool: %v", err)
	}
	for _, migration := range []string{"001_iot.sql", "002_citizen_accounts.sql", "003_operations_admin.sql", "004_password_recovery.sql", "005_payments.sql", "006_resident_locations.sql", "007_area_waste_fees.sql"} {
		path := filepath.Join("..", "..", "db", "migrations", migration)
		contents, readErr := os.ReadFile(path)
		if readErr != nil {
			pool.Close()
			cleanup()
			t.Fatalf("read %s: %v", migration, readErr)
		}
		if _, execErr := pool.Exec(context.Background(), string(contents)); execErr != nil {
			pool.Close()
			cleanup()
			t.Fatalf("apply %s to isolated schema: %v", migration, execErr)
		}
	}
	t.Cleanup(func() { pool.Close() })
	return pool, cleanup
}

func applyManagedAreaMigration(t *testing.T, pool *pgxpool.Pool) error {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join("..", "..", "db", "migrations", "008_managed_service_areas.sql"))
	if err != nil {
		return err
	}
	_, err = pool.Exec(context.Background(), string(contents))
	return err
}

func insertLegacyLocation(t *testing.T, pool *pgxpool.Pool, residentID int64, locality, city, state string) int64 {
	t.Helper()
	var id int64
	err := pool.QueryRow(context.Background(), `INSERT INTO resident_locations(resident_id,label,address_line1,locality,city,state)
		VALUES($1,$2,'1 Legacy Road',$3,$4,$5) RETURNING id`, residentID, locality+" property", locality, city, state).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func callAs(handler http.HandlerFunc, method, path, body string, who user) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if method == http.MethodPatch {
		parts := strings.Split(strings.Trim(path, "/"), "/")
		if len(parts) > 0 {
			req.SetPathValue("id", parts[len(parts)-1])
		}
	}
	req = req.WithContext(context.WithValue(req.Context(), userContextKey, who))
	response := httptest.NewRecorder()
	handler(response, req)
	return response
}

func createArea(t *testing.T, s *server, who user, locality, city, state string) serviceArea {
	t.Helper()
	body := fmt.Sprintf(`{"locality":%q,"city":%q,"state":%q}`, locality, city, state)
	response := callAs(s.createServiceArea, http.MethodPost, "/api/v1/admin/service-areas", body, who)
	if response.Code != http.StatusCreated {
		t.Fatalf("create service area returned %d: %s", response.Code, response.Body.String())
	}
	var payload struct {
		Area serviceArea `json:"service_area"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	return payload.Area
}

func saveFee(t *testing.T, s *server, who user, areaID, amount int64, legacyID int64) int {
	t.Helper()
	body := fmt.Sprintf(`{"area_id":%d,"amount_kobo":%d,"legacy_fee_id":%d}`, areaID, amount, legacyID)
	return callAs(s.saveAreaFee, http.MethodPut, "/api/v1/admin/area-fees", body, who).Code
}

func createResidentLocation(t *testing.T, s *server, who user, areaID int64) int64 {
	t.Helper()
	body := fmt.Sprintf(`{"area_id":%d,"label":"Test home","address_line1":"22 Sample Road","address_line2":"","postal_code":""}`, areaID)
	response := callAs(s.createLocation, http.MethodPost, "/api/v1/locations", body, who)
	if response.Code != http.StatusCreated {
		t.Fatalf("create resident address returned %d: %s", response.Code, response.Body.String())
	}
	var payload struct {
		Location residentLocation `json:"location"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Location.AreaID == nil || *payload.Location.AreaID != areaID {
		t.Fatalf("address returned wrong service area: %+v", payload.Location)
	}
	var canonical serviceArea
	if err := s.db.QueryRow(context.Background(), `SELECT locality,city,state FROM service_areas WHERE id=$1`, areaID).Scan(&canonical.Locality, &canonical.City, &canonical.State); err != nil {
		t.Fatal(err)
	}
	if payload.Location.Locality != canonical.Locality || payload.Location.City != canonical.City || payload.Location.State != canonical.State {
		t.Fatalf("resident-supplied geographic text overrode managed area values: %+v", payload.Location)
	}
	return payload.Location.ID
}

func callLocationCreate(t *testing.T, s *server, who user, body map[string]any) int {
	t.Helper()
	encoded, _ := json.Marshal(body)
	return callAs(s.createLocation, http.MethodPost, "/api/v1/locations", string(encoded), who).Code
}

func paymentOptionStatus(t *testing.T, s *server, who user, locationID int64) int {
	t.Helper()
	return callAs(s.paymentOptions, http.MethodGet, fmt.Sprintf("/api/v1/payments/options?location_id=%d", locationID), "", who).Code
}

func assertOptionFee(t *testing.T, s *server, who user, locationID, areaID, amount int64) {
	t.Helper()
	response := callAs(s.paymentOptions, http.MethodGet, fmt.Sprintf("/api/v1/payments/options?location_id=%d", locationID), "", who)
	if response.Code != http.StatusOK {
		t.Fatalf("payment options returned %d: %s", response.Code, response.Body.String())
	}
	var payload struct {
		AreaID int64 `json:"area_id"`
		Amount int64 `json:"fee_amount_kobo"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil || payload.AreaID != areaID || payload.Amount != amount {
		t.Fatalf("payment options fee mismatch: %+v err=%v", payload, err)
	}
}

func deactivateArea(t *testing.T, s *server, who user, areaID int64) {
	t.Helper()
	body := `{"active":false}`
	response := callAs(s.updateServiceArea, http.MethodPatch, fmt.Sprintf("/api/v1/admin/service-areas/%d", areaID), body, who)
	if response.Code != http.StatusOK {
		t.Fatalf("deactivate service area returned %d: %s", response.Code, response.Body.String())
	}
}
