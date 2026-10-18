package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPropertyUnitAndBinWorkflow(t *testing.T) {
	dsn := testingDatabaseURL(t)
	pool, cleanup := managedAreaTestDatabase(t, dsn)
	defer cleanup()
	if err := applyManagedAreaMigration(t, pool); err != nil {
		t.Fatalf("apply managed service area migration: %v", err)
	}
	s := &server{db: pool}
	ctx := context.Background()
	ownerID := addWorkflowUser(t, pool, "Property Owner", "property-owner@example.test", "citizen")
	tenantID := addWorkflowUser(t, pool, "Property Tenant", "property-tenant@example.test", "citizen")
	staffID := addWorkflowUser(t, pool, "Property Staff", "property-staff@example.test", "operations_manager")
	adminID := addWorkflowUser(t, pool, "Property Admin", "property-admin@example.test", "system_administrator")
	var areaID int64
	if err := pool.QueryRow(ctx, `INSERT INTO service_areas(locality,city,state) VALUES('Test Locality','Test City','Test State') RETURNING id`).Scan(&areaID); err != nil {
		t.Fatal(err)
	}
	ownerLocation := addWorkflowLocation(t, pool, ownerID, areaID)
	tenantLocation := addWorkflowLocation(t, pool, tenantID, areaID)
	propertyBody := fmt.Sprintf(`{"location_id":%d,"property_type":"residential","structure_type":"storey_building","floors":2,"registration_scope":"whole_property","registrant_role":"owner","units":[{"code":"Downstairs Flat","unit_type":"residential","floor_label":"Ground floor","service_scope":"individual_bin"}]}`, ownerLocation)
	created := callAs(s.registerProperty, http.MethodPost, "/api/v1/properties", propertyBody, user{ID: ownerID, Role: "citizen"})
	if created.Code != http.StatusCreated {
		t.Fatalf("property registration status %d: %s", created.Code, created.Body.String())
	}
	residentApplication := callAs(s.listProperties, http.MethodGet, "/api/v1/properties", "", user{ID: ownerID, Role: "citizen"})
	if residentApplication.Code != http.StatusOK || !strings.Contains(residentApplication.Body.String(), `"review_status":"pending_review"`) {
		t.Fatalf("new application should be visible as pending to resident: status=%d response=%s", residentApplication.Code, residentApplication.Body.String())
	}
	ownerDeleteRequest := httptestRequestWithID(t, http.MethodDelete, fmt.Sprintf("/api/v1/locations/%d", ownerLocation), "id", fmt.Sprint(ownerLocation), "", user{ID: ownerID, Role: "citizen"})
	ownerDelete := callHandler(s.deleteLocation, ownerDeleteRequest)
	if ownerDelete.Code != http.StatusConflict {
		t.Fatalf("owner address removal status %d, want 409: %s", ownerDelete.Code, ownerDelete.Body.String())
	}
	lockedAddress := callAs(s.updateLocation, http.MethodPatch, fmt.Sprintf("/api/v1/locations/%d", ownerLocation), fmt.Sprintf(`{"area_id":%d,"label":"Changed","address_line1":"11 Other Street"}`, areaID), user{ID: ownerID, Role: "citizen"})
	if lockedAddress.Code != http.StatusConflict {
		t.Fatalf("registered address edit status %d, want 409: %s", lockedAddress.Code, lockedAddress.Body.String())
	}
	var propertyID int64
	if err := pool.QueryRow(ctx, `SELECT id FROM registered_properties WHERE location_id=$1`, ownerLocation).Scan(&propertyID); err != nil {
		t.Fatal(err)
	}
	managementApplications := callAs(s.operationsProperties, http.MethodGet, "/api/v1/operations/properties", "", user{ID: staffID, Role: "operations_manager"})
	if managementApplications.Code != http.StatusOK || !strings.Contains(managementApplications.Body.String(), `"review_status":"pending_review"`) {
		t.Fatalf("management queue should include the pending application: status=%d response=%s", managementApplications.Code, managementApplications.Body.String())
	}
	missingApplicationReason := callAs(s.reviewPropertyRegistration, http.MethodPatch, fmt.Sprintf("/api/v1/operations/properties/%d/review", propertyID), `{"decision":"approved"}`, user{ID: staffID, Role: "operations_manager"})
	if missingApplicationReason.Code != http.StatusUnprocessableEntity {
		t.Fatalf("application review without reason status %d, want 422: %s", missingApplicationReason.Code, missingApplicationReason.Body.String())
	}
	binBeforeReviewRequest := httptestRequestWithID(t, http.MethodPost, fmt.Sprintf("/api/v1/operations/properties/%d/bins", propertyID), "id", fmt.Sprint(propertyID), `{"tracking_id":"EARLY-BIN-1","unit_id":1,"size_litres":120}`, user{ID: staffID, Role: "operations_manager"})
	binBeforeReview := callHandler(s.createPropertyBin, binBeforeReviewRequest)
	if binBeforeReview.Code != http.StatusNotFound {
		t.Fatalf("bin should not be assigned before application review: status=%d response=%s", binBeforeReview.Code, binBeforeReview.Body.String())
	}
	applicationApprovalReason := "Address and registration details checked with the applicant."
	applicationApproval := httptestRequestWithID(t, http.MethodPatch, fmt.Sprintf("/api/v1/operations/properties/%d/review", propertyID), "id", fmt.Sprint(propertyID), fmt.Sprintf(`{"decision":"approved","reason":%q}`, applicationApprovalReason), user{ID: staffID, Role: "operations_manager"})
	applicationApproved := callHandler(s.reviewPropertyRegistration, applicationApproval)
	if applicationApproved.Code != http.StatusOK || !strings.Contains(applicationApproved.Body.String(), `"status":"awaiting_bin_delivery"`) {
		t.Fatalf("property approval status=%d response=%s", applicationApproved.Code, applicationApproved.Body.String())
	}
	assertAuditRow(t, pool, "property_application_reviewed", "registered_property", fmt.Sprint(propertyID), staffID)
	duplicateBody := strings.Replace(propertyBody, fmt.Sprintf(`"location_id":%d`, ownerLocation), fmt.Sprintf(`"location_id":%d`, tenantLocation), 1)
	duplicate := callAs(s.registerProperty, http.MethodPost, "/api/v1/properties", duplicateBody, user{ID: tenantID, Role: "citizen"})
	if duplicate.Code != http.StatusConflict {
		t.Fatalf("duplicate property address status %d, want 409: %s", duplicate.Code, duplicate.Body.String())
	}
	requestBody := fmt.Sprintf(`{"location_id":%d,"code":"Upstairs Flat","unit_type":"residential","floor_label":"First floor","service_scope":"individual_bin","relationship":"tenant"}`, tenantLocation)
	requested := callAs(s.requestPropertyUnit, http.MethodPost, "/api/v1/properties/unit-requests", requestBody, user{ID: tenantID, Role: "citizen"})
	if requested.Code != http.StatusCreated {
		t.Fatalf("unit request status %d: %s", requested.Code, requested.Body.String())
	}
	var requestID int64
	if err := pool.QueryRow(ctx, `SELECT id FROM property_unit_requests WHERE property_id=$1 AND requester_id=$2 AND status='pending'`, propertyID, tenantID).Scan(&requestID); err != nil {
		t.Fatal(err)
	}
	review := callAs(s.reviewPropertyUnitRequest, http.MethodPatch, fmt.Sprintf("/api/v1/operations/property-unit-requests/%d", requestID), `{"decision":"approved","reason":"Address and unit details confirmed with the resident."}`, user{ID: staffID, Role: "operations_manager"})
	if review.Code != http.StatusOK {
		t.Fatalf("unit approval status %d: %s", review.Code, review.Body.String())
	}
	var savedReason string
	if err := pool.QueryRow(ctx, `SELECT review_reason FROM property_unit_requests WHERE id=$1`, requestID).Scan(&savedReason); err != nil || savedReason != "Address and unit details confirmed with the resident." {
		t.Fatalf("saved review reason=%q err=%v", savedReason, err)
	}
	residentRequests := callAs(s.residentPropertyUnitRequests, http.MethodGet, "/api/v1/properties/unit-requests", "", user{ID: tenantID, Role: "citizen"})
	if residentRequests.Code != http.StatusOK || !strings.Contains(residentRequests.Body.String(), "Address and unit details confirmed with the resident.") {
		t.Fatalf("resident request status=%d response=%s", residentRequests.Code, residentRequests.Body.String())
	}
	residentAddresses := callAs(s.listLocations, http.MethodGet, "/api/v1/locations", "", user{ID: tenantID, Role: "citizen"})
	if residentAddresses.Code != http.StatusOK || !strings.Contains(residentAddresses.Body.String(), `"property_registered":true`) || !strings.Contains(residentAddresses.Body.String(), `"property_relationship":"tenant"`) {
		t.Fatalf("resident address lock metadata status=%d response=%s", residentAddresses.Code, residentAddresses.Body.String())
	}
	secondRequestBody := fmt.Sprintf(`{"location_id":%d,"code":"Rejected Unit","unit_type":"residential","floor_label":"Second floor","service_scope":"individual_bin","relationship":"tenant"}`, tenantLocation)
	secondRequest := callAs(s.requestPropertyUnit, http.MethodPost, "/api/v1/properties/unit-requests", secondRequestBody, user{ID: tenantID, Role: "citizen"})
	if secondRequest.Code != http.StatusCreated {
		t.Fatalf("second unit request status %d: %s", secondRequest.Code, secondRequest.Body.String())
	}
	var secondRequestID int64
	if err := pool.QueryRow(ctx, `SELECT id FROM property_unit_requests WHERE requester_id=$1 AND unit_code='Rejected Unit'`, tenantID).Scan(&secondRequestID); err != nil {
		t.Fatal(err)
	}
	missingReason := callAs(s.reviewPropertyUnitRequest, http.MethodPatch, fmt.Sprintf("/api/v1/operations/property-unit-requests/%d", secondRequestID), `{"decision":"rejected"}`, user{ID: staffID, Role: "operations_manager"})
	if missingReason.Code != http.StatusUnprocessableEntity {
		t.Fatalf("missing rejection reason status %d: %s", missingReason.Code, missingReason.Body.String())
	}
	rejected := callAs(s.reviewPropertyUnitRequest, http.MethodPatch, fmt.Sprintf("/api/v1/operations/property-unit-requests/%d", secondRequestID), `{"decision":"rejected","reason":"Unit details could not be confirmed with the resident."}`, user{ID: staffID, Role: "operations_manager"})
	if rejected.Code != http.StatusOK {
		t.Fatalf("unit rejection status %d: %s", rejected.Code, rejected.Body.String())
	}
	var rejectionReason string
	if err := pool.QueryRow(ctx, `SELECT review_reason FROM property_unit_requests WHERE id=$1`, secondRequestID).Scan(&rejectionReason); err != nil || rejectionReason != "Unit details could not be confirmed with the resident." {
		t.Fatalf("saved rejection reason=%q err=%v", rejectionReason, err)
	}
	var expectedBins int
	if err := pool.QueryRow(ctx, `SELECT expected_bin_count FROM registered_properties WHERE id=$1`, propertyID).Scan(&expectedBins); err != nil || expectedBins != 2 {
		t.Fatalf("expected bin count=%d err=%v, want 2 after approved individual unit", expectedBins, err)
	}
	var units []int64
	rows, err := pool.Query(ctx, `SELECT id FROM property_units WHERE property_id=$1 ORDER BY unit_code`, propertyID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var unitID int64
		if err := rows.Scan(&unitID); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		units = append(units, unitID)
	}
	rows.Close()
	if len(units) != 2 {
		t.Fatalf("property has %d units, want 2", len(units))
	}
	for i, unitID := range units {
		body := fmt.Sprintf(`{"tracking_id":"TEST-BIN-%d","unit_id":%d,"size_litres":120}`, i+1, unitID)
		binReq := httptestRequestWithID(t, http.MethodPost, fmt.Sprintf("/api/v1/operations/properties/%d/bins", propertyID), "id", fmt.Sprint(propertyID), body, user{ID: staffID, Role: "operations_manager"})
		binResponse := callHandler(s.createPropertyBin, binReq)
		if binResponse.Code != http.StatusCreated {
			t.Fatalf("bin registration status %d: %s", binResponse.Code, binResponse.Body.String())
		}
		var binID int64
		if err := pool.QueryRow(ctx, `SELECT id FROM property_bins WHERE tracking_id=$1`, fmt.Sprintf("TEST-BIN-%d", i+1)).Scan(&binID); err != nil {
			t.Fatal(err)
		}
		deliveryReq := httptestRequestWithID(t, http.MethodPatch, fmt.Sprintf("/api/v1/operations/bins/%d/delivery", binID), "id", fmt.Sprint(binID), `{"outcome":"delivered","note":"Handover confirmed"}`, user{ID: staffID, Role: "operations_manager"})
		delivery := callHandler(s.recordBinDelivery, deliveryReq)
		if delivery.Code != http.StatusOK {
			t.Fatalf("bin delivery status %d: %s", delivery.Code, delivery.Body.String())
		}
		if i == 0 {
			var status string
			if err := pool.QueryRow(ctx, `SELECT status FROM registered_properties WHERE id=$1`, propertyID).Scan(&status); err != nil || status != "awaiting_bin_delivery" {
				t.Fatalf("property status after only one of two handovers=%q err=%v", status, err)
			}
		}
	}
	var propertyStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM registered_properties WHERE id=$1`, propertyID).Scan(&propertyStatus); err != nil || propertyStatus != "verified_by_delivery" {
		t.Fatalf("property status after all deliveries=%q err=%v", propertyStatus, err)
	}
	residentPropertyList := callAs(s.listProperties, http.MethodGet, "/api/v1/properties", "", user{ID: ownerID, Role: "citizen"})
	if residentPropertyList.Code != http.StatusOK || !strings.Contains(residentPropertyList.Body.String(), "TEST-BIN-1") || !strings.Contains(residentPropertyList.Body.String(), "delivered_at") || !strings.Contains(residentPropertyList.Body.String(), `"review_status":"verified"`) || !strings.Contains(residentPropertyList.Body.String(), applicationApprovalReason) {
		t.Fatalf("resident bin profile status=%d response=%s", residentPropertyList.Code, residentPropertyList.Body.String())
	}
	moveOutRequest := httptestRequestWithID(t, http.MethodDelete, fmt.Sprintf("/api/v1/locations/%d", tenantLocation), "id", fmt.Sprint(tenantLocation), "", user{ID: tenantID, Role: "citizen"})
	moveOut := callHandler(s.deleteLocation, moveOutRequest)
	if moveOut.Code != http.StatusNoContent {
		t.Fatalf("tenant move-out status %d: %s", moveOut.Code, moveOut.Body.String())
	}
	assertAuditRow(t, pool, "resident_address_removed", "resident_location", fmt.Sprint(tenantLocation), tenantID)
	var preservedAddress, preservedBins bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM resident_locations WHERE id=$1 AND deleted_at IS NOT NULL), EXISTS(SELECT 1 FROM property_bins WHERE property_id=$2 AND tracking_id LIKE 'TEST-BIN-%')`, tenantLocation, propertyID).Scan(&preservedAddress, &preservedBins); err != nil || !preservedAddress || !preservedBins {
		t.Fatalf("move-out must preserve historical address/bin rows: address=%v bins=%v err=%v", preservedAddress, preservedBins, err)
	}
	tenantProperties := callAs(s.listProperties, http.MethodGet, "/api/v1/properties", "", user{ID: tenantID, Role: "citizen"})
	if tenantProperties.Code != http.StatusOK || strings.Contains(tenantProperties.Body.String(), "TEST-BIN-") {
		t.Fatalf("tenant retained active bin profile after move-out: status=%d response=%s", tenantProperties.Code, tenantProperties.Body.String())
	}
	var ended bool
	if err := pool.QueryRow(ctx, `SELECT ended_at IS NOT NULL FROM property_account_memberships WHERE property_id=$1 AND user_id=$2`, propertyID, tenantID).Scan(&ended); err != nil || !ended {
		t.Fatalf("tenant membership ended=%v err=%v", ended, err)
	}
	var tenantPropertyCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM property_account_memberships WHERE property_id=$1 AND user_id=$2 AND ended_at IS NULL`, propertyID, tenantID).Scan(&tenantPropertyCount); err != nil || tenantPropertyCount != 0 {
		t.Fatalf("active tenant membership count=%d err=%v", tenantPropertyCount, err)
	}
	fee := callAs(s.saveAreaBinFee, http.MethodPut, "/api/v1/admin/area-bin-fees", fmt.Sprintf(`{"area_id":%d,"size_litres":120,"amount_kobo":8750}`, areaID), user{ID: adminID, Role: "system_administrator"})
	if fee.Code != http.StatusOK {
		t.Fatalf("area bin fee save status %d: %s", fee.Code, fee.Body.String())
	}
	var amount int64
	if err := pool.QueryRow(ctx, `SELECT amount_kobo FROM area_bin_fees WHERE area_id=$1 AND size_litres=120`, areaID).Scan(&amount); err != nil || amount != 8750 {
		t.Fatalf("stored fee=%d err=%v, want 8750 kobo", amount, err)
	}
	assertAuditRow(t, pool, "bin_delivery_recorded", "property_bin", "2", staffID)
	assistedResident := addWorkflowUser(t, pool, "Assisted Resident", "assisted-resident@example.test", "citizen")
	assistedBody := fmt.Sprintf(`{"resident_email":"assisted-resident@example.test","area_id":%d,"label":"Shop","address_line1":"22 New Street","property_type":"commercial","structure_type":"shop_complex","floors":1,"registration_scope":"single_unit","registrant_role":"tenant","units":[{"code":"Shop A","unit_type":"shop","floor_label":"Ground floor","service_scope":"individual_bin"}]}`, areaID)
	assisted := callAs(s.assistedPropertyRegistration, http.MethodPost, "/api/v1/operations/assisted-properties", assistedBody, user{ID: staffID, Role: "operations_manager"})
	if assisted.Code != http.StatusCreated {
		t.Fatalf("assisted registration status %d: %s", assisted.Code, assisted.Body.String())
	}
	var assistedMemberships int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM property_account_memberships m JOIN registered_properties p ON p.id=m.property_id JOIN property_units u ON u.id=m.unit_id WHERE m.user_id=$1 AND p.address_key='22 new street' AND u.unit_code='Shop A'`, assistedResident).Scan(&assistedMemberships); err != nil || assistedMemberships != 1 {
		t.Fatalf("assisted membership count=%d err=%v", assistedMemberships, err)
	}
	var assistedPropertyID int64
	if err := pool.QueryRow(ctx, `SELECT id FROM registered_properties WHERE address_key='22 new street'`).Scan(&assistedPropertyID); err != nil {
		t.Fatal(err)
	}
	assertAuditRow(t, pool, "assisted_property_registration_created", "registered_property", fmt.Sprint(assistedPropertyID), staffID)

	rejectedResidentID := addWorkflowUser(t, pool, "Rejected Applicant", "rejected-applicant@example.test", "citizen")
	var rejectedLocationID int64
	if err := pool.QueryRow(ctx, `INSERT INTO resident_locations(resident_id,area_id,label,address_line1,address_line2,locality,city,state) VALUES($1,$2,'Home','22 Rejected Street','','Test Locality','Test City','Test State') RETURNING id`, rejectedResidentID, areaID).Scan(&rejectedLocationID); err != nil {
		t.Fatal(err)
	}
	rejectedPropertyBody := strings.Replace(propertyBody, fmt.Sprintf(`"location_id":%d`, ownerLocation), fmt.Sprintf(`"location_id":%d`, rejectedLocationID), 1)
	rejectedApplication := callAs(s.registerProperty, http.MethodPost, "/api/v1/properties", rejectedPropertyBody, user{ID: rejectedResidentID, Role: "citizen"})
	if rejectedApplication.Code != http.StatusCreated {
		t.Fatalf("second property registration status %d: %s", rejectedApplication.Code, rejectedApplication.Body.String())
	}
	var rejectedPropertyID int64
	if err := pool.QueryRow(ctx, `SELECT id FROM registered_properties WHERE location_id=$1`, rejectedLocationID).Scan(&rejectedPropertyID); err != nil {
		t.Fatal(err)
	}
	propertyRejectionReason := "The submitted address details could not be confirmed."
	propertyRejectRequest := httptestRequestWithID(t, http.MethodPatch, fmt.Sprintf("/api/v1/operations/properties/%d/review", rejectedPropertyID), "id", fmt.Sprint(rejectedPropertyID), fmt.Sprintf(`{"decision":"rejected","reason":%q}`, propertyRejectionReason), user{ID: staffID, Role: "operations_manager"})
	propertyRejected := callHandler(s.reviewPropertyRegistration, propertyRejectRequest)
	if propertyRejected.Code != http.StatusOK || !strings.Contains(propertyRejected.Body.String(), `"status":"rejected"`) {
		t.Fatalf("property rejection status=%d response=%s", propertyRejected.Code, propertyRejected.Body.String())
	}
	residentRejectedApplication := callAs(s.listProperties, http.MethodGet, "/api/v1/properties", "", user{ID: rejectedResidentID, Role: "citizen"})
	if residentRejectedApplication.Code != http.StatusOK || !strings.Contains(residentRejectedApplication.Body.String(), `"review_status":"rejected"`) || !strings.Contains(residentRejectedApplication.Body.String(), propertyRejectionReason) {
		t.Fatalf("resident should see rejection status and reason: status=%d response=%s", residentRejectedApplication.Code, residentRejectedApplication.Body.String())
	}
	assertAuditRow(t, pool, "property_application_reviewed", "registered_property", fmt.Sprint(rejectedPropertyID), staffID)
}

func addWorkflowUser(t *testing.T, pool *pgxpool.Pool, name, email, role string) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(context.Background(), `INSERT INTO users(full_name,email,phone,password_hash,role) VALUES($1,$2,'+234000000000','test-hash',$3) RETURNING id`, name, email, role).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func addWorkflowLocation(t *testing.T, pool *pgxpool.Pool, residentID, areaID int64) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(context.Background(), `INSERT INTO resident_locations(resident_id,area_id,label,address_line1,address_line2,locality,city,state) VALUES($1,$2,'Home','10 Test Street','','Test Locality','Test City','Test State') RETURNING id`, residentID, areaID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func httptestRequestWithID(t *testing.T, method, path, key, value, body string, who user) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue(key, value)
	return req.WithContext(context.WithValue(req.Context(), userContextKey, who))
}

func callHandler(handler http.HandlerFunc, req *http.Request) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	handler(recorder, req)
	return recorder
}
