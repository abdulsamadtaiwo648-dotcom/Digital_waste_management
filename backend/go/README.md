# Backend services

Go runs three independently configured APIs from the same codebase: `SERVICE_ROLE=resident` on port 8080, `SERVICE_ROLE=management` on port 8081, and `SERVICE_ROLE=admin` on port 8082. Each process exposes only its route group, checks a distinct frontend origin, and uses a separate staff session cookie. The resident API handles resident identity, property addresses, complaints, and payments; management handles resident service activity and operations; admin handles staff accounts, devices, settings, and audit records. The processes currently use one PostgreSQL database so service activity is available to authorized management routes. Python owns only the calibrated sensor distance validation and fill calculation and has no database access. The processor binds to loopback by default; set `PROCESSOR_BIND_HOST` to a private interface address when it must accept requests from a separate Go host. Do not expose this unauthenticated calculation service publicly.

## Local prerequisites

- Go 1.24 or newer
- Python 3.11 or newer (standard library only)
- PostgreSQL 15 or newer

Create a database and apply migrations `001_iot.sql` through `008_managed_service_areas.sql` in numeric order from `db/migrations`. Each API process needs `DATABASE_URL`, `SESSION_SECRET` (at least 32 characters), `SERVICE_ROLE`, and its own `SERVICE_ORIGIN`. The Go service does not load `.env` files. Run `go run ./backend/go` in three processes with `SERVICE_ROLE` set to `resident`, `management`, and `admin`; `PORT` can override each service's default port.

Create the first administrator once, after applying migrations. Set `BOOTSTRAP_ADMIN_PASSWORD` and run `go run ./backend/go bootstrap-admin "Full Name" "admin@example.test" "+234..."`. There is no default password, and bootstrap refuses to run when an active system administrator exists. Enable `DEV_RESET_LINKS=true` only in local development to show a one-time recovery token in the UI; real email delivery is not configured.

Devices are provisioned in the system administration workspace. Their raw token is shown once, and only its SHA-256 hash is stored. Requests require both `X-Device-ID` and `X-Device-Token`; the body must identify the same device. Simulator state comes from the registered device record and cannot be asserted by the incoming reading. No physical sensor is currently connected.

Resident accounts can own multiple labeled locations such as a home and shop. New complaints and payments must reference a location belonging to the signed-in resident, and the management payment and complaint endpoints include the saved service address. Payment records use integer kobo and are scoped to the resident who created them. The simulation flow is available only outside production when `PAYMENT_MODE=simulation` and an administrator has configured a positive `waste_fee_kobo`. It creates a demonstration receipt and explicitly records that no funds moved. A real payment provider is not connected; production payment initiation remains disabled.

## IoT endpoint

`POST /api/v1/iot/readings` accepts JSON `{ "device_id": "...", "measured_at": "RFC3339 timestamp", "distance_cm": 42.5 }` plus the two device headers. Go reads the registered skip's dimensions and thresholds, delegates fill calculation to Python, persists only valid readings, and creates an operational notification when the status crosses into warning or critical. Duplicate device/timestamp pairs return a conflict. `GET /healthz` checks PostgreSQL connectivity.

Go/Python unit tests do not need a database. Account and workflow routes require PostgreSQL migrations to be applied.
