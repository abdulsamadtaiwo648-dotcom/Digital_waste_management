#!/usr/bin/env python3
"""
============================================================================
 WASTE BIN SENSOR SIMULATOR v1.1
 Digital Waste Management System — Presentation & Testing Tool
============================================================================
 Simulates the ESP32-C3 waste bin sensor for demos and backend testing.
 Uses only Python standard library — no pip installs required.

 Features:
   - Clear separation of Sensor Fill State vs Backend HTTP Status
   - Default uplink endpoint: http://localhost:8082/api/v1/iot/readings
   - Presentation transmission: Uploads every 10 readings or immediately on alert
   - Emulates LittleFS offline flash buffering between sync intervals
   - Interactive simulation modes: normal, rapid, theft, intermittent, demo

 Usage:
   python sensor_simulator.py
   python sensor_simulator.py --mode theft
   python sensor_simulator.py --device-id DEV-XXXXX --device-token <token>
   python sensor_simulator.py --api-url none   (pure offline demo)
============================================================================
"""

import argparse
import json
import math
import os
import random
import re
import sys
import time
from datetime import datetime, timezone, timedelta
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen

# ============================================================================
#  ANSI COLOR CODES (works on Windows 10+ and Unix terminals)
# ============================================================================
class Colors:
    RESET      = "\033[0m"
    BOLD       = "\033[1m"
    DIM        = "\033[2m"
    RED        = "\033[91m"
    GREEN      = "\033[92m"
    YELLOW     = "\033[93m"
    BLUE       = "\033[94m"
    MAGENTA    = "\033[95m"
    CYAN       = "\033[96m"
    WHITE      = "\033[97m"
    BG_RED     = "\033[41m"
    BG_GREEN   = "\033[42m"
    BG_YELLOW  = "\033[43m"
    BG_MAGENTA = "\033[45m"

# Enable ANSI escape codes on Windows
if sys.platform == "win32":
    os.system("")

# ============================================================================
#  SENSOR ALERT CODES (Internal Hardware / Firmware Status)
# ============================================================================
CODE_NORMAL       = 200
CODE_WARNING      = 300
CODE_CRITICAL     = 500
CODE_TAMPER       = 600
CODE_SENSOR_ERROR = 700

ALERT_NAMES = {
    CODE_NORMAL:       "NORMAL (Fill < 70%)",
    CODE_WARNING:      "WARNING (Fill ≥ 70%)",
    CODE_CRITICAL:     "CRITICAL (Fill ≥ 90%)",
    CODE_TAMPER:       "TAMPER / STOLEN (Geotag mismatch)",
    CODE_SENSOR_ERROR: "SENSOR ERROR (Out of bounds)",
}

ALERT_COLORS = {
    CODE_NORMAL:       Colors.GREEN,
    CODE_WARNING:      Colors.YELLOW,
    CODE_CRITICAL:     Colors.RED,
    CODE_TAMPER:       Colors.MAGENTA,
    CODE_SENSOR_ERROR: Colors.RED,
}

ALERT_SYMBOLS = {
    CODE_NORMAL:       "✓",
    CODE_WARNING:      "⚠",
    CODE_CRITICAL:     "✗",
    CODE_TAMPER:       "⚡",
    CODE_SENSOR_ERROR: "✗",
}

# Box drawing characters
BOX_TL = "╔"; BOX_TR = "╗"; BOX_BL = "╚"; BOX_BR = "╝"
BOX_H  = "═"; BOX_V  = "║"; BOX_ML = "╠"; BOX_MR = "╣"
BOX_HL = "─"

WIDTH = 66  # Inner width of display box

# ============================================================================
#  SIMULATOR STATE
# ============================================================================
class SensorState:
    def __init__(self, args):
        self.device_id       = args.device_id
        self.device_token    = args.device_token
        self.bin_depth_cm    = args.bin_depth
        self.warning_thresh  = 70
        self.critical_thresh = 90
        self.api_url         = "" if args.api_url.lower() in ("none", "off", "disable", "disabled") else args.api_url
        self.interval        = args.interval
        self.mode            = args.mode
        self.sync_interval   = 10  # Transmit every 10 readings for presentation

        # Simulated measurements
        self.fill_level      = 0.0
        self.battery_voltage = 4.15
        self.latitude        = args.lat
        self.longitude       = args.lng
        self.reg_lat         = args.lat
        self.reg_lng         = args.lng
        self.reading_count   = 0
        self.pending_buffer  = []
        self.connection_ok   = True
        self.tamper_active   = False
        self.log_file        = os.path.join(
            os.path.dirname(os.path.abspath(__file__)), "sensor_log.jsonl"
        )

        # HTTP Network status tracking (separated from sensor state)
        self.last_http_status  = "Standby (Awaiting first reading)"
        self.last_http_code    = None
        self.last_http_success = None
        self.total_sent        = 0
        self.total_failed      = 0

        # Mode-specific control
        self._theft_triggered  = False
        self._fill_rate        = 2.0
        self._readings_in_mode = 0

    def reset_for_mode(self, mode):
        """Reset state for a new simulation mode."""
        self.mode = mode
        self.fill_level = random.uniform(5, 20)
        self.battery_voltage = random.uniform(3.85, 4.15)
        self.latitude = self.reg_lat
        self.longitude = self.reg_lng
        self.tamper_active = False
        self._theft_triggered = False
        self._readings_in_mode = 0
        self.connection_ok = True

        if mode == "normal":
            self._fill_rate = random.uniform(1.2, 2.5)
        elif mode == "rapid":
            self._fill_rate = random.uniform(5.5, 9.0)
        elif mode == "theft":
            self._fill_rate = random.uniform(1.0, 2.0)
        elif mode == "intermittent":
            self._fill_rate = random.uniform(2.0, 4.0)


# ============================================================================
#  SIMULATION HELPERS
# ============================================================================
def simulate_ultrasonic(state):
    """Simulate HC-SR04 ultrasonic distance reading with sensor noise."""
    state.fill_level = max(0.0, min(100.0, state.fill_level))
    ideal_distance = state.bin_depth_cm * (1.0 - (state.fill_level / 100.0))
    noise = random.gauss(0, 0.7)
    return round(max(2.0, ideal_distance + noise), 1)


def simulate_fill_progression(state):
    """Progress the fill level according to current mode."""
    state._readings_in_mode += 1

    if state.mode == "normal":
        inc = state._fill_rate + random.uniform(-0.4, 0.8)
        state.fill_level = min(100.0, state.fill_level + inc)

    elif state.mode == "rapid":
        inc = state._fill_rate + random.uniform(0.5, 3.0)
        state.fill_level = min(100.0, state.fill_level + inc)

    elif state.mode == "theft":
        inc = state._fill_rate + random.uniform(-0.3, 0.5)
        state.fill_level = min(100.0, state.fill_level + inc)
        # Drift away after 8 readings
        if state._readings_in_mode >= 8 and not state._theft_triggered:
            state._theft_triggered = True
            state.tamper_active = True
            state.latitude += random.uniform(0.0025, 0.0050)
            state.longitude += random.uniform(0.0025, 0.0050)

    elif state.mode == "intermittent":
        inc = state._fill_rate + random.uniform(-0.5, 1.5)
        state.fill_level = min(100.0, state.fill_level + inc)
        state.connection_ok = random.random() > 0.45


def simulate_battery_drain(state):
    """Simulate realistic battery drain."""
    drain = random.uniform(0.001, 0.004)
    state.battery_voltage = max(3.0, state.battery_voltage - drain)


def haversine_distance(lat1, lon1, lat2, lon2):
    """Calculate distance between GPS points in meters."""
    R = 6371000
    phi1, phi2 = math.radians(lat1), math.radians(lat2)
    dphi = math.radians(lat2 - lat1)
    dlambda = math.radians(lon2 - lon1)
    a = (math.sin(dphi / 2) ** 2 +
         math.cos(phi1) * math.cos(phi2) * math.sin(dlambda / 2) ** 2)
    return R * 2 * math.atan2(math.sqrt(a), math.sqrt(1 - a))


def determine_alert_code(state, fill_percent, distance):
    """Determine sensor hardware alert code."""
    if state.tamper_active:
        drift = haversine_distance(state.reg_lat, state.reg_lng, state.latitude, state.longitude)
        if drift > 50:
            return CODE_TAMPER

    if distance < 0 or distance > state.bin_depth_cm + 15:
        return CODE_SENSOR_ERROR

    if fill_percent >= state.critical_thresh:
        return CODE_CRITICAL
    if fill_percent >= state.warning_thresh:
        return CODE_WARNING

    return CODE_NORMAL


def battery_percent(voltage):
    """Estimate battery percentage."""
    if voltage >= 4.15:
        return 100
    if voltage <= 3.2:
        return 0
    return int(((voltage - 3.2) / 0.95) * 100)


# ============================================================================
#  NETWORK COMMUNICATION & HTTP STATUS TRACKING
# ============================================================================
def send_to_backend(state, reading_data):
    """Send reading payload to Go backend API and record HTTP response."""
    if not state.api_url:
        state.last_http_status = "Offline Mode (No API URL set)"
        state.last_http_code = None
        state.last_http_success = None
        return False

    payload = json.dumps({
        "device_id":   reading_data["device_id"],
        "measured_at": reading_data["measured_at"],
        "distance_cm": reading_data["distance_cm"],
    }).encode("utf-8")

    request = Request(
        state.api_url,
        data=payload,
        headers={
            "Content-Type": "application/json",
            "X-Device-ID": state.device_id,
            "X-Device-Token": state.device_token,
        },
    )

    try:
        with urlopen(request, timeout=4) as response:
            code = response.getcode()
            state.last_http_code = code
            state.last_http_status = f"HTTP {code} Created (Uplink OK)" if code == 201 else f"HTTP {code} OK"
            state.last_http_success = True
            state.total_sent += 1
            return True

    except HTTPError as e:
        state.last_http_code = e.code
        state.last_http_success = False
        state.total_failed += 1
        err_detail = ""
        try:
            body = json.loads(e.read().decode("utf-8", errors="replace"))
            err_detail = body.get("message") or body.get("error") or ""
        except Exception:
            pass

        if e.code == 401:
            state.last_http_status = f"HTTP 401 Unauthorized (Invalid Device ID/Token in DB)"
        elif e.code == 409:
            state.last_http_status = f"HTTP 409 Conflict (Duplicate timestamp)"
        elif e.code == 422:
            state.last_http_status = f"HTTP 422 Unprocessable ({err_detail or 'Check limits'})"
        else:
            state.last_http_status = f"HTTP {e.code} Error ({err_detail or e.reason})"
        return False

    except URLError:
        state.last_http_code = None
        state.last_http_success = False
        state.last_http_status = "Connection Refused (Backend not running on port 8082)"
        return False

    except TimeoutError:
        state.last_http_code = None
        state.last_http_success = False
        state.last_http_status = "HTTP Timeout (>4s response time)"
        return False

    except Exception as exc:
        state.last_http_code = None
        state.last_http_success = False
        state.last_http_status = f"Network Error ({type(exc).__name__})"
        return False


def flush_pending(state):
    """Flush readings buffered in LittleFS flash memory."""
    if not state.pending_buffer:
        return

    remaining = []
    flushed_count = 0
    for reading in state.pending_buffer:
        if send_to_backend(state, reading):
            flushed_count += 1
        else:
            remaining.append(reading)

    state.pending_buffer = remaining
    if flushed_count > 0:
        state.last_http_status += f" [Flushed {flushed_count} from LittleFS]"


# ============================================================================
#  LOCAL LOGGING (LittleFS Simulation)
# ============================================================================
def log_to_file(state, reading_data):
    try:
        with open(state.log_file, "a", encoding="utf-8") as f:
            f.write(json.dumps(reading_data) + "\n")
    except OSError:
        pass


# ============================================================================
#  DISPLAY RENDERING
# ============================================================================
def clear_screen():
    os.system("cls" if sys.platform == "win32" else "clear")


def render_box_line(content, color=""):
    reset = Colors.RESET if color else ""
    clean = re.sub(r'\033\[[0-9;]*m', '', content)
    padding = max(0, WIDTH - len(clean))
    return f"{BOX_V}  {color}{content}{reset}{' ' * padding}  {BOX_V}"


def render_fill_bar(fill_percent, bar_width=22):
    filled = int((fill_percent / 100.0) * bar_width)
    empty = max(0, bar_width - filled)
    if fill_percent >= 90:
        color = Colors.RED
    elif fill_percent >= 70:
        color = Colors.YELLOW
    else:
        color = Colors.GREEN
    return f"{color}{'█' * filled}{Colors.DIM}{'░' * empty}{Colors.RESET}", color


def render_reading(state, distance, fill_percent, alert_code, timestamp, sync_triggered):
    clear_screen()

    alert_color = ALERT_COLORS.get(alert_code, Colors.WHITE)
    alert_name = ALERT_NAMES.get(alert_code, "UNKNOWN")
    alert_symbol = ALERT_SYMBOLS.get(alert_code, "?")
    fill_bar, bar_color = render_fill_bar(fill_percent)
    batt_pct = battery_percent(state.battery_voltage)

    # GPS info
    drift = haversine_distance(state.reg_lat, state.reg_lng, state.latitude, state.longitude)
    if state.tamper_active:
        gps_display = f"{Colors.MAGENTA}⚡ MOVED (+{drift:.0f}m from station){Colors.RESET}"
    else:
        gps_display = f"{Colors.GREEN}✓ Position Verified (Drift: {drift:.1f}m){Colors.RESET}"

    # HTTP Status Color
    if state.last_http_success is True:
        http_color = Colors.GREEN
        http_icon = "●"
    elif state.last_http_success is False:
        http_color = Colors.RED
        http_icon = "✗"
    else:
        http_color = Colors.CYAN
        http_icon = "○"

    # Uplink countdown
    next_sync = state.sync_interval - (state.reading_count % state.sync_interval)
    if next_sync == state.sync_interval:
        next_sync = 0

    lines = []
    lines.append(f"\n  {BOX_TL}{BOX_H * (WIDTH + 4)}{BOX_TR}")
    lines.append(render_box_line(f"{Colors.BOLD}{Colors.CYAN}SMART WASTE SENSOR SIMULATOR v1.1{Colors.RESET}"))
    lines.append(render_box_line(f"Device: {Colors.BOLD}{state.device_id}{Colors.RESET}   Mode: {Colors.YELLOW}{state.mode.upper()}{Colors.RESET}   Interval: {state.interval}s"))
    lines.append(f"  {BOX_ML}{BOX_H * (WIDTH + 4)}{BOX_MR}")

    # Top Header
    lines.append(render_box_line(f"Reading #{state.reading_count:<6}  Measured At: {timestamp}"))
    lines.append(render_box_line(f"{BOX_HL * 56}"))

    # Section 1: SENSOR FILL STATE
    lines.append(render_box_line(f"{Colors.BOLD}{Colors.WHITE}[ SENSOR MEASUREMENT & BIN LEVEL ]{Colors.RESET}"))
    lines.append(render_box_line(f"Distance:      {Colors.BOLD}{distance:>6.1f} cm{Colors.RESET}"))
    lines.append(render_box_line(f"Fill Level:    {fill_bar} {bar_color}{fill_percent:>5.1f}%{Colors.RESET}"))
    lines.append(render_box_line(f"Sensor State:  {alert_color}{Colors.BOLD}{alert_symbol} {alert_name}{Colors.RESET}"))
    lines.append(render_box_line(f"Firmware Code: {alert_color}{Colors.BOLD}Code {alert_code}{Colors.RESET} (Local threshold logic)"))
    lines.append(render_box_line(f"GPS Status:    {gps_display}"))
    lines.append(render_box_line(f"Battery:       {state.battery_voltage:.2f}V ({batt_pct}%)"))

    lines.append(render_box_line(f"{BOX_HL * 56}"))

    # Section 2: BACKEND HTTP & NETWORK STATUS
    lines.append(render_box_line(f"{Colors.BOLD}{Colors.WHITE}[ BACKEND HTTP & NETWORK UPLINK ]{Colors.RESET}"))
    api_show = state.api_url if state.api_url else "(Disabled - Pure local demo)"
    lines.append(render_box_line(f"Target API:    {api_show}"))
    lines.append(render_box_line(f"Sync Policy:   Every {state.sync_interval} readings (or immediate on Alert ≥ 300)"))

    if sync_triggered:
        sync_tag = f"{Colors.YELLOW}[UPLINK ACTIVE THIS CYCLE]{Colors.RESET}"
    else:
        sync_tag = f"Next upload in {next_sync} reading(s)"
    lines.append(render_box_line(f"Transmission:  {sync_tag}"))

    lines.append(render_box_line(f"HTTP Status:   {http_color}{http_icon} {state.last_http_status}{Colors.RESET}"))
    lines.append(render_box_line(f"LittleFS Queue:{len(state.pending_buffer)} buffered reading(s) stored in flash"))

    lines.append(f"  {BOX_BL}{BOX_H * (WIDTH + 4)}{BOX_BR}")

    # History Mini-Log
    lines.append(f"\n  {Colors.BOLD}Telemetry History (Last 5 Readings):{Colors.RESET}")
    print("\n".join(lines))


def render_mode_banner(mode, description):
    clear_screen()
    print(f"\n  {Colors.BOLD}{Colors.CYAN}{'═' * 55}{Colors.RESET}")
    print(f"  {Colors.BOLD}  Switching to: {Colors.YELLOW}{mode.upper()}{Colors.RESET} {Colors.BOLD}mode{Colors.RESET}")
    print(f"  {Colors.DIM}  {description}{Colors.RESET}")
    print(f"  {Colors.BOLD}{Colors.CYAN}{'═' * 55}{Colors.RESET}\n")
    time.sleep(1.5)


# ============================================================================
#  MAIN SIMULATION LOOP
# ============================================================================
def run_simulation(state):
    mode_descriptions = {
        "normal":       "Bin gradually fills over time (typical residential pattern)",
        "rapid":        "Bin fills quickly (commercial / market area)",
        "theft":        "Bin GPS moves away after 8 cycles (triggers Code 600 tamper)",
        "intermittent": "Network connection drops randomly (simulates weak cell/WiFi)",
        "demo":         "Cycles through all operational scenarios automatically",
    }

    demo_modes = ["normal", "rapid", "theft", "intermittent"]
    demo_mode_index = 0
    readings_per_demo_mode = 18

    if state.mode == "demo":
        state.reset_for_mode(demo_modes[demo_mode_index])
        render_mode_banner(demo_modes[demo_mode_index], mode_descriptions[demo_modes[demo_mode_index]])
    else:
        state.reset_for_mode(state.mode)
        render_mode_banner(state.mode, mode_descriptions.get(state.mode, ""))

    history = []

    try:
        while True:
            state.reading_count += 1

            # Progress simulated physics
            simulate_fill_progression(state)
            simulate_battery_drain(state)
            distance = simulate_ultrasonic(state)

            fill_percent = ((state.bin_depth_cm - distance) / state.bin_depth_cm) * 100.0
            fill_percent = max(0.0, min(100.0, fill_percent))

            alert_code = determine_alert_code(state, fill_percent, distance)

            now = datetime.now(timezone(timedelta(hours=1)))
            timestamp = now.strftime("%Y-%m-%d %H:%M:%S")
            iso_timestamp = now.isoformat()

            reading_data = {
                "device_id":       state.device_id,
                "measured_at":     iso_timestamp,
                "distance_cm":     round(distance, 2),
                "fill_percent":    round(fill_percent, 2),
                "alert_code":      alert_code,
                "latitude":        round(state.latitude, 6),
                "longitude":       round(state.longitude, 6),
                "battery_voltage": round(state.battery_voltage, 2),
                "is_simulated":    True,
            }

            # Always save to local flash log (LittleFS simulation)
            log_to_file(state, reading_data)

            # Transmission policy:
            # Transmit every 10 readings (presentation mode) OR immediately when Alert >= 300
            sync_triggered = (state.reading_count % state.sync_interval == 0) or (alert_code >= CODE_WARNING)

            if sync_triggered and state.connection_ok:
                sent = send_to_backend(state, reading_data)
                if sent:
                    flush_pending(state)
                else:
                    state.pending_buffer.append(reading_data)
            elif not state.connection_ok:
                state.last_http_status = "Network Unavailable (Buffering in flash)"
                state.last_http_success = False
                state.pending_buffer.append(reading_data)
            else:
                # Normal reading within the 10-reading interval: buffer in flash
                state.pending_buffer.append(reading_data)
                next_in = state.sync_interval - (state.reading_count % state.sync_interval)
                state.last_http_status = f"Standby (Buffering in flash; next sync in {next_in} reading{'s' if next_in > 1 else ''})"
                state.last_http_success = None

            # Render full UI
            render_reading(state, distance, fill_percent, alert_code, timestamp, sync_triggered)

            # Format mini-history log
            alert_c = ALERT_COLORS.get(alert_code, "")
            http_brief = state.last_http_status[:32]
            h_line = (
                f"  {Colors.DIM}#{state.reading_count:>3}{Colors.RESET}  "
                f"{timestamp[11:]}  "
                f"{alert_c}{fill_percent:>5.1f}%{Colors.RESET} "
                f"({alert_c}Code {alert_code}{Colors.RESET})  "
                f"│ {Colors.CYAN}{http_brief}{Colors.RESET}"
            )
            history.append(h_line)
            for item in history[-5:]:
                print(item)

            print(f"\n  {Colors.DIM}Next reading in {state.interval}s... (Press Ctrl+C to stop){Colors.RESET}")

            # Demo mode scenario transition
            if state.mode == "demo":
                if state._readings_in_mode >= readings_per_demo_mode:
                    demo_mode_index = (demo_mode_index + 1) % len(demo_modes)
                    state.reset_for_mode(demo_modes[demo_mode_index])
                    render_mode_banner(demo_modes[demo_mode_index], mode_descriptions[demo_modes[demo_mode_index]])
                    history = []

            # Auto-empty bin when full
            if state.fill_level >= 100.0 and state.mode != "theft":
                time.sleep(state.interval)
                state.fill_level = random.uniform(2.0, 10.0)
                print(f"\n  {Colors.GREEN}{Colors.BOLD}🚛 Collection Truck Emptied the Bin!{Colors.RESET}\n")
                time.sleep(1.5)
                continue

            time.sleep(state.interval)

    except KeyboardInterrupt:
        print(f"\n\n  {Colors.YELLOW}Simulator stopped.{Colors.RESET}")
        print(f"  Total readings: {state.reading_count}")
        print(f"  Readings sent:  {state.total_sent}")
        print(f"  Flash log:      {state.log_file}")
        print(f"  Pending flash:  {len(state.pending_buffer)}\n")


# ============================================================================
#  CLI ARGUMENT PARSER
# ============================================================================
def parse_args():
    default_url = os.getenv("API_URL", "http://localhost:8082/api/v1/iot/readings")

    parser = argparse.ArgumentParser(
        description="Smart Waste Bin Sensor Simulator — Digital Waste Management System",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
Alert Codes (Firmware State):
  200  NORMAL   — Bin level is below warning threshold (< 70%)
  300  WARNING  — Bin level is between 70% and 90%
  500  CRITICAL — Bin level is >= 90% (Urgent pickup required)
  600  TAMPER   — Bin moved >50m from registered station (Theft alert)
  700  ERROR    — Sensor reading out-of-range

Uplink Policy:
  - Normal readings (Code 200) are buffered in flash and transmitted every 10 cycles.
  - Alert readings (Code 300, 500, 600) trigger immediate uplink and flush the buffer.
  - Default API target: http://localhost:8082/api/v1/iot/readings

Examples:
  python sensor_simulator.py
  python sensor_simulator.py --mode theft
  python sensor_simulator.py --device-id DEV-001 --device-token <token>
  python sensor_simulator.py --api-url none (Run standalone without backend)
        """,
    )

    parser.add_argument(
        "--mode", choices=["normal", "rapid", "theft", "intermittent", "demo"],
        default="demo", help="Simulation scenario (default: demo)"
    )
    parser.add_argument(
        "--interval", type=int, default=3,
        help="Seconds between sensor readings (default: 3s)"
    )
    parser.add_argument(
        "--api-url", type=str,
        default=default_url,
        help=f"Target backend endpoint (default: {default_url})"
    )
    parser.add_argument(
        "--device-id", type=str,
        default=os.getenv("SIMULATOR_DEVICE_ID", "sensor-001"),
        help="Device ID (must match registered device in Admin, default: sensor-001)"
    )
    parser.add_argument(
        "--device-token", type=str,
        default=os.getenv("SIMULATOR_DEVICE_TOKEN", "sim-token-001"),
        help="Device authentication token (from Admin registration)"
    )
    parser.add_argument(
        "--bin-depth", type=float,
        default=float(os.getenv("SIMULATOR_DEPTH_CM", "100")),
        help="Bin depth in cm (default: 100cm)"
    )
    parser.add_argument(
        "--lat", type=float, default=6.5244,
        help="Registered latitude (default: 6.5244 — Lagos)"
    )
    parser.add_argument(
        "--lng", type=float, default=3.3792,
        help="Registered longitude (default: 3.3792 — Lagos)"
    )

    return parser.parse_args()


# ============================================================================
#  ENTRY POINT
# ============================================================================
def main():
    args = parse_args()
    state = SensorState(args)

    print(f"\n  {Colors.BOLD}{Colors.CYAN}{'═' * 55}{Colors.RESET}")
    print(f"  {Colors.BOLD}  SMART WASTE SENSOR SIMULATOR v1.1{Colors.RESET}")
    print(f"  {Colors.BOLD}  Digital Waste Management System{Colors.RESET}")
    print(f"  {Colors.CYAN}{'═' * 55}{Colors.RESET}")
    print(f"  {Colors.DIM}Device ID:   {state.device_id}{Colors.RESET}")
    print(f"  {Colors.DIM}Mode:        {state.mode}{Colors.RESET}")
    print(f"  {Colors.DIM}Interval:    {state.interval}s{Colors.RESET}")
    print(f"  {Colors.DIM}Bin Depth:   {state.bin_depth_cm} cm{Colors.RESET}")
    print(f"  {Colors.DIM}Target API:  {state.api_url or '(Disabled - Standalone)'}{Colors.RESET}")
    print(f"  {Colors.DIM}Sync Policy: Transmit every {state.sync_interval} readings or on alert{Colors.RESET}")
    print(f"  {Colors.CYAN}{'═' * 55}{Colors.RESET}\n")
    time.sleep(1.5)

    run_simulation(state)


if __name__ == "__main__":
    main()
