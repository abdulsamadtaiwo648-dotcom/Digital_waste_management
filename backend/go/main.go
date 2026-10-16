package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type server struct {
	db                *pgxpool.Pool
	processorURL      string
	processorClient   *http.Client
	sessionSecret     []byte
	serviceRole       string
	serviceOrigin     string
	staffCookie       string
	cookieSecure      bool
	authLimiter       *authLimiter
	devResetLinks     bool
	paymentSimulation bool
}

type readingRequest struct {
	DeviceID   string    `json:"device_id"`
	MeasuredAt time.Time `json:"measured_at"`
	DistanceCM float64   `json:"distance_cm"`
}

type calculation struct {
	DistanceCM           float64 `json:"distance_cm"`
	UsableDepthCM        float64 `json:"usable_depth_cm"`
	MinDistanceCM        float64 `json:"min_distance_cm"`
	InstallationOffsetCM float64 `json:"installation_offset_cm"`
	WarningThreshold     float64 `json:"warning_threshold"`
	CriticalThreshold    float64 `json:"critical_threshold"`
}

type calculatedReading struct {
	FillPercent float64 `json:"fill_percent"`
	Level       string  `json:"level"`
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if len(os.Args) > 1 && os.Args[1] == "bootstrap-admin" {
		if err := bootstrapAdmin(os.Args[2:]); err != nil {
			logger.Error("administrator bootstrap failed", "error", err)
			os.Exit(1)
		}
		logger.Info("first administrator account created")
		return
	}
	dsn := os.Getenv("DATABASE_URL")
	sessionSecret := os.Getenv("SESSION_SECRET")
	processorURL := strings.TrimRight(os.Getenv("PYTHON_PROCESSOR_URL"), "/")
	if dsn == "" || len(sessionSecret) < 32 {
		logger.Error("DATABASE_URL and a SESSION_SECRET of at least 32 characters must be set")
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := pgxpool.New(ctx, dsn)
	if err != nil {
		logger.Error("connect to database", "error", err)
		os.Exit(1)
	}
	defer db.Close()
	if err := db.Ping(ctx); err != nil {
		logger.Error("ping database", "error", err)
		os.Exit(1)
	}

	production := os.Getenv("APP_ENV") == "production"
	serviceRole := envOr("SERVICE_ROLE", "resident")
	if serviceRole != "resident" && serviceRole != "management" && serviceRole != "admin" {
		logger.Error("SERVICE_ROLE must be resident, management, or admin")
		os.Exit(1)
	}
	staffCookie := "dwmp_staff_session"
	if serviceRole == "management" {
		staffCookie = "dwmp_management_session"
	}
	if serviceRole == "admin" {
		staffCookie = "dwmp_admin_session"
	}
	s := &server{db: db, processorURL: processorURL, processorClient: &http.Client{Timeout: 3 * time.Second}, sessionSecret: []byte(sessionSecret), serviceRole: serviceRole, serviceOrigin: envOr("SERVICE_ORIGIN", envOr("FRONTEND_ORIGIN", "http://localhost:5173")), staffCookie: staffCookie, cookieSecure: os.Getenv("COOKIE_SECURE") == "true" || production, authLimiter: newAuthLimiter(), devResetLinks: !production && os.Getenv("DEV_RESET_LINKS") == "true", paymentSimulation: !production && os.Getenv("PAYMENT_MODE") == "simulation"}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /service-info", s.serviceInfo)
	mux.HandleFunc("POST /api/v1/iot/readings", s.ingestReading)
	mux.Handle("POST /api/v1/auth/register", s.limitAuth(http.HandlerFunc(s.register)))
	mux.Handle("POST /api/v1/auth/login", s.limitAuth(http.HandlerFunc(s.login)))
	mux.Handle("POST /api/v1/auth/staff/login", s.limitAuth(http.HandlerFunc(s.staffLogin)))
	mux.Handle("POST /api/v1/auth/forgot-password", s.limitAuth(http.HandlerFunc(s.forgotPassword)))
	mux.Handle("POST /api/v1/auth/reset-password", s.limitAuth(http.HandlerFunc(s.resetPassword)))
	mux.HandleFunc("POST /api/v1/auth/logout", s.logout)
	mux.HandleFunc("POST /api/v1/auth/staff/logout", s.staffLogout)
	mux.Handle("GET /api/v1/auth/me", s.requireUser(http.HandlerFunc(s.me)))
	mux.Handle("GET /api/v1/auth/staff/me", s.requireStaff(http.HandlerFunc(s.staffMe)))
	mux.Handle("PATCH /api/v1/auth/me", s.requireUser(http.HandlerFunc(s.updateProfile)))
	mux.Handle("GET /api/v1/locations", s.requireUser(http.HandlerFunc(s.listLocations)))
	mux.Handle("GET /api/v1/service-areas", s.requireUser(http.HandlerFunc(s.listResidentServiceAreas)))
	mux.Handle("POST /api/v1/locations", s.requireUser(http.HandlerFunc(s.createLocation)))
	mux.Handle("PATCH /api/v1/locations/{id}", s.requireUser(http.HandlerFunc(s.updateLocation)))
	mux.Handle("DELETE /api/v1/locations/{id}", s.requireUser(http.HandlerFunc(s.deleteLocation)))
	mux.Handle("GET /api/v1/payments/options", s.requireUser(http.HandlerFunc(s.paymentOptions)))
	mux.Handle("GET /api/v1/payments", s.requireUser(http.HandlerFunc(s.listPayments)))
	mux.Handle("POST /api/v1/payments", s.requireUser(s.limitAuth(http.HandlerFunc(s.createPayment))))
	mux.Handle("GET /api/v1/payments/{reference}", s.requireUser(http.HandlerFunc(s.getPayment)))
	mux.Handle("POST /api/v1/payments/{reference}/simulate", s.requireUser(http.HandlerFunc(s.simulatePayment)))
	mux.Handle("GET /api/v1/receipts/{reference}", s.requireUser(http.HandlerFunc(s.getReceipt)))
	mux.Handle("GET /api/v1/complaints", s.requireUser(http.HandlerFunc(s.listComplaints)))
	mux.Handle("POST /api/v1/complaints", s.requireUser(http.HandlerFunc(s.createComplaint)))
	mux.Handle("GET /api/v1/complaints/{reference}", s.requireUser(http.HandlerFunc(s.getComplaint)))
	mux.Handle("GET /api/v1/announcements", http.HandlerFunc(s.publicAnnouncements))
	mux.Handle("GET /api/v1/operations/complaints", s.requireRole("operations_manager", http.HandlerFunc(s.operationsComplaints)))
	mux.Handle("GET /api/v1/operations/payments", s.requireRole("operations_manager", http.HandlerFunc(s.operationsPayments)))
	mux.Handle("PATCH /api/v1/operations/complaints/{reference}", s.requireRole("operations_manager", http.HandlerFunc(s.updateComplaintStatus)))
	mux.Handle("GET /api/v1/operations/containers", s.requireRole("operations_manager", http.HandlerFunc(s.listContainers)))
	mux.Handle("POST /api/v1/operations/containers", s.requireRole("operations_manager", http.HandlerFunc(s.createContainer)))
	mux.Handle("GET /api/v1/operations/collections", s.requireRole("operations_manager", http.HandlerFunc(s.listCollections)))
	mux.Handle("POST /api/v1/operations/collections", s.requireRole("operations_manager", http.HandlerFunc(s.createCollection)))
	mux.Handle("PATCH /api/v1/operations/collections/{id}", s.requireRole("operations_manager", http.HandlerFunc(s.updateCollection)))
	mux.Handle("GET /api/v1/operations/inspections", s.requireRole("operations_manager", http.HandlerFunc(s.listInspections)))
	mux.Handle("POST /api/v1/operations/inspections", s.requireRole("operations_manager", http.HandlerFunc(s.createInspection)))
	mux.Handle("GET /api/v1/operations/staff", s.requireRole("operations_manager", http.HandlerFunc(s.operationsStaff)))
	mux.Handle("GET /api/v1/operations/analytics", s.requireRole("operations_manager", http.HandlerFunc(s.operationsAnalytics)))
	mux.Handle("GET /api/v1/operations/announcements", s.requireRole("operations_manager", http.HandlerFunc(s.listAnnouncements)))
	mux.Handle("POST /api/v1/operations/announcements", s.requireRole("operations_manager", http.HandlerFunc(s.createAnnouncement)))
	mux.Handle("GET /api/v1/operations/notifications", s.requireRole("operations_manager", http.HandlerFunc(s.listNotifications)))
	mux.Handle("GET /api/v1/admin/users", s.requireRole("system_administrator", http.HandlerFunc(s.adminUsers)))
	mux.Handle("POST /api/v1/admin/users", s.requireRole("system_administrator", http.HandlerFunc(s.createStaff)))
	mux.Handle("PATCH /api/v1/admin/users/{id}", s.requireRole("system_administrator", http.HandlerFunc(s.updateStaff)))
	mux.Handle("GET /api/v1/admin/devices", s.requireRole("system_administrator", http.HandlerFunc(s.adminDevices)))
	mux.Handle("GET /api/v1/admin/containers", s.requireRole("system_administrator", http.HandlerFunc(s.listContainers)))
	mux.Handle("POST /api/v1/admin/devices", s.requireRole("system_administrator", http.HandlerFunc(s.createDevice)))
	mux.Handle("PATCH /api/v1/admin/devices/{id}", s.requireRole("system_administrator", http.HandlerFunc(s.updateDevice)))
	mux.Handle("GET /api/v1/admin/audit-logs", s.requireRole("system_administrator", http.HandlerFunc(s.auditLogs)))
	mux.Handle("GET /api/v1/admin/settings", s.requireRole("system_administrator", http.HandlerFunc(s.getSettings)))
	mux.Handle("PATCH /api/v1/admin/settings", s.requireRole("system_administrator", http.HandlerFunc(s.updateSettings)))
	mux.Handle("GET /api/v1/admin/area-fees", s.requireRole("system_administrator", http.HandlerFunc(s.listAreaFees)))
	mux.Handle("PUT /api/v1/admin/area-fees", s.requireRole("system_administrator", http.HandlerFunc(s.saveAreaFee)))
	mux.Handle("GET /api/v1/admin/service-areas", s.requireRole("system_administrator", http.HandlerFunc(s.listAdminServiceAreas)))
	mux.Handle("POST /api/v1/admin/service-areas", s.requireRole("system_administrator", http.HandlerFunc(s.createServiceArea)))
	mux.Handle("PATCH /api/v1/admin/service-areas/{id}", s.requireRole("system_administrator", http.HandlerFunc(s.updateServiceArea)))
	addr := ":" + envOr("PORT", servicePort(serviceRole))
	logger.Info("Go API listening", "address", addr)
	if err := http.ListenAndServe(addr, logRequests(logger, s.cors(s.onlyServiceRoutes(mux)))); err != nil {
		logger.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func servicePort(role string) string {
	switch role {
	case "resident":
		return "8080"
	case "management":
		return "8081"
	case "admin":
		return "8082"
	default:
		return "8080"
	}
}

// Each deployment exposes only its own API surface. Persistence is shared so
// staff can act on resident requests without a common browser-facing API.
func (s *server) onlyServiceRoutes(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		allowed := path == "/healthz" || path == "/service-info"
		switch s.serviceRole {
		case "resident":
			allowed = allowed || (!strings.HasPrefix(path, "/api/v1/admin/") && !strings.HasPrefix(path, "/api/v1/operations/") && !strings.HasPrefix(path, "/api/v1/auth/staff/") && path != "/api/v1/iot/readings")
		case "management":
			allowed = allowed || strings.HasPrefix(path, "/api/v1/operations/") || strings.HasPrefix(path, "/api/v1/auth/staff/") || path == "/api/v1/announcements"
		case "admin":
			allowed = allowed || strings.HasPrefix(path, "/api/v1/admin/") || strings.HasPrefix(path, "/api/v1/auth/staff/") || path == "/api/v1/iot/readings"
		}
		if !allowed {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *server) health(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Service-Role", s.serviceRole)
	if err := s.db.Ping(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, "database_unavailable", "Database is unavailable")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) serviceInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"service_role": s.serviceRole})
}

func (s *server) ingestReading(w http.ResponseWriter, r *http.Request) {
	providedID := r.Header.Get("X-Device-ID")
	providedToken := r.Header.Get("X-Device-Token")
	if providedID == "" || providedToken == "" {
		writeError(w, http.StatusUnauthorized, "invalid_device_credentials", "Device credentials are invalid")
		return
	}
	var req readingRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.DeviceID == "" || req.DeviceID != providedID || req.DistanceCM < 0 || req.MeasuredAt.IsZero() {
		writeError(w, http.StatusUnprocessableEntity, "invalid_reading", "device_id, a non-negative distance_cm, and measured_at are required")
		return
	}
	if s.processorURL == "" {
		writeError(w, http.StatusServiceUnavailable, "processor_unconfigured", "IoT reading processing is not configured")
		return
	}
	var input calculation
	if req.MeasuredAt.After(time.Now().Add(5*time.Minute)) || req.MeasuredAt.Before(time.Now().Add(-24*time.Hour)) {
		writeError(w, http.StatusUnprocessableEntity, "invalid_timestamp", "Reading timestamp is outside the accepted time window")
		return
	}
	var storedHash string
	var simulated bool
	err := s.db.QueryRow(r.Context(), `SELECT d.token_hash, d.is_simulated, c.usable_depth_cm, c.minimum_distance_cm, c.installation_offset_cm
		FROM devices d JOIN containers c ON c.id=d.container_id WHERE d.id=$1 AND d.enabled=true`, req.DeviceID).
		Scan(&storedHash, &simulated, &input.UsableDepthCM, &input.MinDistanceCM, &input.InstallationOffsetCM)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusUnauthorized, "invalid_device_credentials", "Device credentials are invalid")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "Device configuration could not be loaded")
		return
	}
	if !deviceTokenMatches(providedToken, storedHash) {
		writeError(w, http.StatusUnauthorized, "invalid_device_credentials", "Device credentials are invalid")
		return
	}
	input.DistanceCM = req.DistanceCM
	input.WarningThreshold, input.CriticalThreshold, err = s.fillThresholds(r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "settings_unavailable", "Sensor thresholds could not be loaded")
		return
	}
	encoded, _ := json.Marshal(input)
	calcReq, _ := http.NewRequestWithContext(r.Context(), http.MethodPost, s.processorURL+"/v1/fill", strings.NewReader(string(encoded)))
	calcReq.Header.Set("Content-Type", "application/json")
	calcResp, err := s.processorClient.Do(calcReq)
	if err != nil {
		writeError(w, http.StatusBadGateway, "processor_unavailable", "Reading processor is unavailable")
		return
	}
	defer calcResp.Body.Close()
	if calcResp.StatusCode != http.StatusOK {
		writeError(w, http.StatusUnprocessableEntity, "invalid_measurement", "Measurement is outside the configured sensor limits")
		return
	}
	var result calculatedReading
	if err := json.NewDecoder(calcResp.Body).Decode(&result); err != nil {
		writeError(w, http.StatusBadGateway, "invalid_processor_response", "Reading processor returned an invalid response")
		return
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "reading_persistence_failed", "Reading could not be saved")
		return
	}
	defer tx.Rollback(r.Context())
	var lockedTokenHash string
	var lockedSimulated bool
	err = tx.QueryRow(r.Context(), `SELECT token_hash,is_simulated FROM devices WHERE id=$1 AND enabled=true FOR UPDATE`, req.DeviceID).Scan(&lockedTokenHash, &lockedSimulated)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusUnauthorized, "invalid_device_credentials", "Device credentials are invalid")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "reading_persistence_failed", "Device credentials could not be revalidated")
		return
	}
	if !deviceTokenMatches(providedToken, lockedTokenHash) {
		writeError(w, http.StatusUnauthorized, "invalid_device_credentials", "Device credentials are invalid")
		return
	}
	simulated = lockedSimulated
	previousLevel := ""
	var previousAt time.Time
	prevErr := tx.QueryRow(r.Context(), `SELECT level,measured_at FROM sensor_readings WHERE device_id=$1 ORDER BY measured_at DESC LIMIT 1`, req.DeviceID).Scan(&previousLevel, &previousAt)
	if prevErr != nil && !errors.Is(prevErr, pgx.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, "reading_persistence_failed", "Previous reading could not be loaded")
		return
	}
	var id int64
	err = tx.QueryRow(r.Context(), `INSERT INTO sensor_readings (device_id, measured_at, distance_cm, fill_percent, level, is_simulated)
		VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`, req.DeviceID, req.MeasuredAt.UTC(), req.DistanceCM, result.FillPercent, result.Level, simulated).Scan(&id)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			writeError(w, http.StatusConflict, "duplicate_reading", "A reading already exists for this device and timestamp")
			return
		}
		writeError(w, http.StatusInternalServerError, "reading_persistence_failed", "Reading could not be saved")
		return
	}
	if (result.Level == "warning" || result.Level == "critical") && result.Level != previousLevel && (prevErr != nil || req.MeasuredAt.After(previousAt)) {
		var label string
		if err := tx.QueryRow(r.Context(), `SELECT c.label FROM devices d JOIN containers c ON c.id=d.container_id WHERE d.id=$1`, req.DeviceID).Scan(&label); err != nil {
			writeError(w, http.StatusInternalServerError, "reading_persistence_failed", "Container details for the reading alert could not be loaded")
			return
		}
		dayKey := req.MeasuredAt.UTC().Format("2006-01-02")
		key := req.DeviceID + ":" + result.Level + ":" + dayKey
		_, err = tx.Exec(r.Context(), `INSERT INTO notifications(target_role,title,message,resource_type,resource_id,dedupe_key) VALUES('operations_manager',$1,$2,'device',$3,$4) ON CONFLICT(dedupe_key) DO NOTHING`, strings.ToUpper(result.Level)+" waste level", label+" reported a "+result.Level+" fill level.", req.DeviceID, key)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "reading_persistence_failed", "Reading alert could not be saved")
			return
		}
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "reading_persistence_failed", "Reading could not be saved")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "device_id": req.DeviceID, "measured_at": req.MeasuredAt.UTC(), "fill_percent": result.FillPercent, "level": result.Level, "simulated": simulated})
}

func deviceTokenMatches(providedToken, storedHash string) bool {
	providedHash := sha256.Sum256([]byte(providedToken))
	storedBytes, err := hex.DecodeString(storedHash)
	return err == nil && len(storedBytes) == len(providedHash) && subtle.ConstantTimeCompare(providedHash[:], storedBytes) == 1
}

func (s *server) fillThresholds(r *http.Request) (float64, float64, error) {
	warning, critical := 70.0, 90.0
	if err := s.db.QueryRow(r.Context(), `SELECT COALESCE((SELECT value::text::numeric FROM application_settings WHERE key='fill_warning_threshold'),70),COALESCE((SELECT value::text::numeric FROM application_settings WHERE key='fill_critical_threshold'),90)`).Scan(&warning, &critical); err != nil {
		return 0, 0, err
	}
	if warning <= 0 || critical <= warning || critical > 100 {
		return 70, 90, nil
	}
	return warning, critical, nil
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code, "message": message})
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
func logRequests(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		logger.Info("http request", "method", r.Method, "path", r.URL.Path, "duration_ms", time.Since(start).Milliseconds())
	})
}
