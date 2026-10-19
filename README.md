# Digital Waste Management Platform

## Architecture

- **Go** owns the REST API, account/session security, authorization, PostgreSQL persistence, complaint workflows, staff workflows, device provisioning, and audit records.
- **Python** owns the calibrated IoT fill calculation. It has no database access and receives measurements from the Go API over an internal HTTP boundary.
- **React, Vite, and TypeScript** build three independent entry pages: the public resident website, private management software, and private administrator software.
- **Go** runs as three isolated API processes (`resident`, `management`, and `admin`). Each process has its own port, CORS origin, route surface, and staff session cookie. The resident API owns registration, saved properties, complaints, and payments; management endpoints expose the resident activity staff need to process; the admin API handles staff, devices, settings, and audit records.
- **PostgreSQL** is shared by the API processes for this local deployment, so resident complaints and payments are visible to authorized staff workflows. The APIs do not share a browser-facing route surface. Apply SQL migrations in numeric order from `db/migrations`.

The implementation includes citizen registration/login, local password recovery, complaint reporting and status tracking, operations complaint/container/collection/inspection/announcement workflows, administrator staff/device/settings/audit workflows, and authenticated sensor ingestion. Online payment processing still needs an approved provider. Hardware and email delivery are not connected. The local development database and its credentials live under the ignored `.local` directory.

## Local prerequisites

- Go 1.24 or newer
- Python 3.11 or newer
- Node.js 20 or newer
- PostgreSQL 15 or newer

Create a PostgreSQL database, then apply each migration in order. In PowerShell, after setting `DATABASE_URL`:

```powershell
psql $env:DATABASE_URL -f db/migrations/001_iot.sql
psql $env:DATABASE_URL -f db/migrations/002_citizen_accounts.sql
psql $env:DATABASE_URL -f db/migrations/003_operations_admin.sql
psql $env:DATABASE_URL -f db/migrations/004_password_recovery.sql
psql $env:DATABASE_URL -f db/migrations/005_payments.sql
psql $env:DATABASE_URL -f db/migrations/006_resident_locations.sql
psql $env:DATABASE_URL -f db/migrations/007_area_waste_fees.sql
psql $env:DATABASE_URL -f db/migrations/008_managed_service_areas.sql
psql $env:DATABASE_URL -f db/migrations/009_property_unit_registration.sql
psql $env:DATABASE_URL -f db/migrations/010_property_lifecycle_and_reviews.sql
psql $env:DATABASE_URL -f db/migrations/011_property_application_review.sql
psql $env:DATABASE_URL -f db/migrations/012_property_unit_case_insensitive_uniqueness.sql
psql $env:DATABASE_URL -f db/migrations/013_bin_account_and_evacuation.sql
```

Migration `008_managed_service_areas.sql` creates managed areas keyed by locality, city and state. It attaches legacy locations using that full tuple and attaches a legacy area fee only when its locality matches exactly one known service area. Ambiguous and unmatched fees remain visible as unresolved records in Admin → Settings; an administrator must confirm the correct locality, city and state before assigning one. If the verified area does not exist yet, create it in Managed service areas first. The migration keeps fee rows and historical payment amounts.

Set backend environment variables before starting Go. Go reads process environment variables directly and does not load `.env` files. `frontend/.env.example` contains only the frontend API URL build variables.

```powershell
$env:DATABASE_URL = 'postgres://dwmp:your-local-password@localhost:5432/dwmp?sslmode=disable'
$env:SESSION_SECRET = 'replace-with-at-least-32-random-characters'
$env:SERVICE_ROLE = 'resident' # resident, management, or admin
$env:SERVICE_ORIGIN = 'http://localhost:5173'
$env:PYTHON_PROCESSOR_URL = 'http://localhost:8090'
$env:APP_ENV = 'development'
$env:DEV_RESET_LINKS = 'true'
$env:PAYMENT_MODE = 'simulation'
```

After the local PostgreSQL binaries and database are initialized, apply migrations `001_iot.sql` through `005_payments.sql` to a fresh database. The local startup script applies the additive location, fee, managed-area, property, and bin-evacuation migrations (`006` through `013`), then starts the processor, APIs, and frontends:

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\scripts\start-local.ps1
```

The script starts PostgreSQL, the Python processor, all three Go API processes, and three Vite entry points when their ports are not already running. Open the resident website at `http://localhost:5173`, management software at `http://localhost:5174/management.html#/login`, and admin software at `http://localhost:5175/admin.html#/login`. Staff accounts are restricted to the matching workspace and use separate session cookies. Local database files and generated development secrets are under `.local`, which is excluded from Git.

For a hosted setup, run the Go service three times with `SERVICE_ROLE=resident|management|admin`, a matching `SERVICE_ORIGIN`, and separate API hostnames. Set `VITE_RESIDENT_API_URL`, `VITE_MANAGEMENT_API_URL`, and `VITE_ADMIN_API_URL` in `frontend/.env` before building the static pages. The production database remains shared in this deployment so authorized management can access resident complaints and payment records.

Create the first administrator once, after migrations. There are no default credentials. Set a strong `BOOTSTRAP_ADMIN_PASSWORD`, then run `go run ./backend/go bootstrap-admin "Full Name" "admin@example.test" "+234..."`. The command refuses to run once an active system administrator exists.

Citizen registration is public; staff accounts can only be created in the administrator workspace. For local password recovery, keep `APP_ENV=development` and explicitly set `DEV_RESET_LINKS=true`; the one-time token is shown in the local UI. Do not enable this option in production. Email delivery is not configured, so production recovery reports that it is unavailable.

Device credentials are provisioned in the administrator workspace. The raw token is returned once; only its SHA-256 hash is stored. Mark simulator devices during registration so their saved readings are always identified as simulated. To send test readings, explicitly set `SIMULATOR_ENABLED=true`, provide the simulator device ID and token, then run `python backend/python/simulator.py`. The physical sensor and transport have not been verified.

Each service area has one 60 L base fee. The charge for each delivered bin assigned to a resident scales in proportion to the bin's capacity: a 120 L bin costs twice the base fee, and the resident's total is the sum of their assigned bin prices. There is no additional area fee. Payment requests stay disabled unless an administrator configures the area's base fee and `PAYMENT_MODE=simulation` is enabled outside production. A resident can then complete a clearly marked demonstration payment; the system records that no funds moved. No real gateway or personal account is used.

## Render resident demo

`render.yaml` creates only the resident website and resident API. The management and admin workspaces are intentionally excluded from this public demo. The resident API uses a database URL entered in Render and starts in production mode with real payments and development password-reset links disabled. Do not connect a database containing real resident records. Create an empty demo database, apply migrations `001_iot.sql` through `013_bin_account_and_evacuation.sql` in order, then enter its connection URL as the `DATABASE_URL` secret when Render prompts for it. Enter the deployed resident website origin as `SERVICE_ORIGIN` when prompted. Render generates the session secret. The sensor processor is not deployed in this resident-only demo.

In Render, create a Blueprint from this repository and select the `wonder` branch. The Blueprint creates a free Go web service and a free static site; free services can sleep when idle and have usage limits. Keep management and admin local until private access has been arranged.

## Verification

```powershell
go test ./...
python -m unittest discover -s backend/python
cd frontend
npm run build
```

Go and Python unit tests do not need a database. Account, role, complaint, collections, and device workflows require PostgreSQL migrations.
#
