package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestIoTIngestionRejectsTrailingJSONValues(t *testing.T) {
	measuredAt := time.Now().UTC().Format(time.RFC3339Nano)
	body := `{"device_id":"SENSOR-1","measured_at":"` + measuredAt + `","distance_cm":42}` + ` {"device_id":"SENSOR-2"}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/iot/readings", strings.NewReader(body))
	request.Header.Set("X-Device-ID", "SENSOR-1")
	request.Header.Set("X-Device-Token", "opaque-test-token")
	response := httptest.NewRecorder()
	(&server{}).ingestReading(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("IoT request with a trailing JSON value returned %d, want 400: %s", response.Code, response.Body.String())
	}
}
