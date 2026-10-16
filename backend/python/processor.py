"""Small HTTP service for calibrated sensor readings; contains no database access."""
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import math
import os


def calculate_fill(distance_cm, usable_depth_cm, minimum_distance_cm, installation_offset_cm,
                   warning_threshold=70, critical_threshold=90):
    values = (distance_cm, usable_depth_cm, minimum_distance_cm, installation_offset_cm,
              warning_threshold, critical_threshold)
    if not all(math.isfinite(value) for value in values):
        raise ValueError("measurements must be finite numbers")
    if usable_depth_cm <= 0 or minimum_distance_cm < 0 or installation_offset_cm < 0:
        raise ValueError("container and sensor configuration is invalid")
    if warning_threshold <= 0 or critical_threshold <= warning_threshold or critical_threshold > 100:
        raise ValueError("fill thresholds are invalid")
    corrected_distance = distance_cm - installation_offset_cm
    if corrected_distance < minimum_distance_cm or corrected_distance > usable_depth_cm:
        raise ValueError("distance is outside sensor or container limits")
    fill = ((usable_depth_cm - corrected_distance) / usable_depth_cm) * 100
    fill = min(100.0, max(0.0, fill))
    level = "critical" if fill >= critical_threshold else "warning" if fill >= warning_threshold else "normal"
    return {"fill_percent": round(fill, 2), "level": level}


class Handler(BaseHTTPRequestHandler):
    def do_POST(self):
        if self.path != "/v1/fill":
            return self.reply(404, {"error": "not_found"})
        try:
            length = int(self.headers.get("Content-Length", "0"))
            if length > 4096 or length <= 0:
                return self.reply(400, {"error": "invalid_request_size"})
            payload = json.loads(self.rfile.read(length))
            result = calculate_fill(
                float(payload["distance_cm"]), float(payload["usable_depth_cm"]),
                float(payload["min_distance_cm"]), float(payload["installation_offset_cm"]),
                float(payload.get("warning_threshold", 70)), float(payload.get("critical_threshold", 90)),
            )
            self.reply(200, result)
        except (ValueError, TypeError, KeyError, json.JSONDecodeError) as exc:
            self.reply(422, {"error": "invalid_measurement", "message": str(exc)})

    def do_GET(self):
        self.reply(204 if self.path == "/healthz" else 404, None)

    def reply(self, status, payload):
        self.send_response(status)
        if payload is not None:
            body = json.dumps(payload).encode()
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
        else:
            body = b""
        self.end_headers()
        if body:
            self.wfile.write(body)


if __name__ == "__main__":
    bind_host = os.getenv("PROCESSOR_BIND_HOST", "127.0.0.1")
    ThreadingHTTPServer((bind_host, int(os.getenv("PORT", "8090"))), Handler).serve_forever()
