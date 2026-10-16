"""Development-only reading simulator. It never claims to represent hardware."""
import json
import os
import random
import time
from datetime import datetime, timezone
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen


def main():
    if os.getenv("SIMULATOR_ENABLED") != "true":
        raise SystemExit("Set SIMULATOR_ENABLED=true to run the development simulator.")
    api_url = os.getenv("API_URL", "http://localhost:8082/api/v1").rstrip("/")
    device_id = os.getenv("SIMULATOR_DEVICE_ID", "")
    token = os.getenv("SIMULATOR_DEVICE_TOKEN", "")
    depth = float(os.getenv("SIMULATOR_DEPTH_CM", "100"))
    minimum = float(os.getenv("SIMULATOR_MIN_DISTANCE_CM", "2"))
    offset = float(os.getenv("SIMULATOR_INSTALLATION_OFFSET_CM", "0"))
    interval = max(5, int(os.getenv("SIMULATOR_INTERVAL_SECONDS", "30")))
    if not device_id or not token or depth <= minimum + offset:
        raise SystemExit("Provide the simulated device ID/token and valid container dimensions.")

    print(f"Sending clearly simulated readings for device {device_id}; press Ctrl+C to stop.")
    while True:
        # Keep some readings near thresholds to exercise warning/critical transitions.
        fill = random.choice([random.uniform(0, 65), random.uniform(67, 96)])
        usable_surface_distance = depth * (1 - fill / 100)
        measured_distance = offset + max(minimum, usable_surface_distance)
        payload = json.dumps({
            "device_id": device_id,
            "measured_at": datetime.now(timezone.utc).isoformat(),
            "distance_cm": round(measured_distance, 2),
        }).encode()
        request = Request(
            f"{api_url}/iot/readings",
            data=payload,
            headers={"Content-Type": "application/json", "X-Device-ID": device_id, "X-Device-Token": token},
        )
        try:
            with urlopen(request, timeout=5) as response:
                result = json.loads(response.read())
                print(json.dumps({"device_id": device_id, "simulated": result.get("simulated"), "fill_percent": result.get("fill_percent"), "level": result.get("level")}))
        except (HTTPError, URLError, TimeoutError) as error:
            print(f"Reading was not accepted: {error}")
        time.sleep(interval)


if __name__ == "__main__":
    main()
