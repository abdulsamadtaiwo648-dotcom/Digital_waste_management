package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestIngestRejectsDeviceRevokedDuringProcessing(t *testing.T) {
	pool, cleanup := managedAreaTestDatabase(t, testingDatabaseURL(t))
	defer cleanup()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO containers(id,label,service_area,usable_depth_cm,minimum_distance_cm,installation_offset_cm)
		VALUES('IOT-RACE-CONTAINER','Test Container','Test Area',100,5,0)`); err != nil {
		t.Fatal(err)
	}

	for _, revoke := range []struct {
		name        string
		SQL         string
		tokenRotate bool
	}{
		{name: "token rotated", SQL: `UPDATE devices SET token_hash=$1 WHERE id=$2`, tokenRotate: true},
		{name: "device disabled", SQL: `UPDATE devices SET enabled=false WHERE id=$1`},
	} {
		t.Run(revoke.name, func(t *testing.T) {
			deviceID := "IOT-RACE-" + strings.ToUpper(strings.ReplaceAll(revoke.name, " ", "-"))
			oldToken := "old-token-for-" + deviceID
			oldHash := sha256.Sum256([]byte(oldToken))
			if _, err := pool.Exec(ctx, `INSERT INTO devices(id,container_id,token_hash,is_simulated,enabled) VALUES($1,'IOT-RACE-CONTAINER',$2,false,true)`, deviceID, hex.EncodeToString(oldHash[:])); err != nil {
				t.Fatal(err)
			}

			processorEntered := make(chan struct{})
			resumeProcessor := make(chan struct{})
			processor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				close(processorEntered)
				<-resumeProcessor
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"fill_percent":50,"level":"normal"}`))
			}))
			defer processor.Close()

			api := &server{db: pool, processorURL: processor.URL, processorClient: processor.Client()}
			reading, err := json.Marshal(readingRequest{DeviceID: deviceID, MeasuredAt: time.Now().UTC(), DistanceCM: 45})
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, "/api/v1/iot/readings", strings.NewReader(string(reading)))
			request.Header.Set("X-Device-ID", deviceID)
			request.Header.Set("X-Device-Token", oldToken)
			response := httptest.NewRecorder()
			done := make(chan struct{})
			go func() {
				api.ingestReading(response, request)
				close(done)
			}()

			select {
			case <-processorEntered:
			case <-time.After(5 * time.Second):
				close(resumeProcessor)
				t.Fatal("reading request did not reach the processor")
			}

			if revoke.tokenRotate {
				newHash := sha256.Sum256([]byte("new-token-for-" + deviceID))
				if _, err := pool.Exec(ctx, revoke.SQL, hex.EncodeToString(newHash[:]), deviceID); err != nil {
					close(resumeProcessor)
					t.Fatal(err)
				}
			} else if _, err := pool.Exec(ctx, revoke.SQL, deviceID); err != nil {
				close(resumeProcessor)
				t.Fatal(err)
			}
			close(resumeProcessor)

			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("reading request did not finish after processor resumed")
			}
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("revoked device request returned %d, want 401: %s", response.Code, response.Body.String())
			}
			var readings int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM sensor_readings WHERE device_id=$1`, deviceID).Scan(&readings); err != nil || readings != 0 {
				t.Fatalf("reading from revoked device was persisted: count=%d err=%v", readings, err)
			}
		})
	}
}

func TestSettingsReadFailuresAreSurfaced(t *testing.T) {
	pool, cleanup := managedAreaTestDatabase(t, testingDatabaseURL(t))
	defer cleanup()
	if _, err := pool.Exec(context.Background(), `ALTER TABLE application_settings RENAME TO application_settings_unavailable_for_test`); err != nil {
		t.Fatal(err)
	}
	s := &server{db: pool}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/operations/analytics", nil)
	request = request.WithContext(context.WithValue(request.Context(), userContextKey, user{ID: 1, Role: "operations_manager"}))
	response := httptest.NewRecorder()
	s.operationsAnalytics(response, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("analytics settings read failure returned %d, want 500", response.Code)
	}
	if _, _, err := s.fillThresholds(httptest.NewRequest(http.MethodPost, "/api/v1/iot/readings", nil)); err == nil {
		t.Fatal("sensor thresholds silently fell back after a database read failure")
	}
	settingsRequest := callAs(s.updateSettings, http.MethodPatch, "/api/v1/admin/settings", `{"fill_warning_threshold":75}`, user{ID: 1, Role: "system_administrator"})
	if settingsRequest.Code != http.StatusInternalServerError {
		t.Fatalf("settings update ignored failed current-value read: got %d want 500", settingsRequest.Code)
	}
}
