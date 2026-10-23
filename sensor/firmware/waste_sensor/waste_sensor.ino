/*
 * ============================================================================
 *  WASTE BIN LEVEL SENSOR — ESP32-C3 SuperMini Firmware
 * ============================================================================
 *  Project:    Digital Waste Management System
 *  Hardware:   ESP32-C3 SuperMini + HC-SR04 Ultrasonic + LittleFS
 *  Version:    1.0.0
 *  License:    MIT
 * 
 *  ALERT CODES:
 *    200 = Normal (bin level OK)
 *    300 = Warning (bin level >= warning threshold)
 *    500 = Critical/Full (bin level >= critical threshold)
 *    600 = Tamper/Stolen (device moved from registered location)
 *    700 = Sensor Error (reading out of range or malfunction)
 * 
 *  WORKFLOW:
 *    1. Wake from deep sleep
 *    2. Read ultrasonic sensor (5 samples, median filter)
 *    3. Calculate fill percentage
 *    4. Check geolocation against registered position (WiFi BSSID scan)
 *    5. Determine alert code
 *    6. Log reading to LittleFS
 *    7. If alert >= 300, attempt to send via WiFi
 *    8. Flush any pending offline logs on successful connection
 *    9. Return to deep sleep
 * ============================================================================
 */

#include <Arduino.h>
#include <WiFi.h>
#include <HTTPClient.h>
#include <LittleFS.h>
#include <ArduinoJson.h>
#include <esp_sleep.h>
#include <esp_task_wdt.h>
#include <math.h>
#include <time.h>

// ============================================================================
//  PIN DEFINITIONS — ESP32-C3 SuperMini
// ============================================================================
#define ULTRASONIC_TRIG_PIN   2
#define ULTRASONIC_ECHO_PIN   3
#define SENSOR_POWER_PIN      4   // GPIO to control power to HC-SR04 via MOSFET
#define LED_BUILTIN_PIN       8   // Built-in LED on ESP32-C3 SuperMini
#define BATTERY_ADC_PIN       0   // ADC1_CH0 for battery voltage divider

// ============================================================================
//  CONSTANTS
// ============================================================================
#define FIRMWARE_VERSION      "1.0.0"
#define CONFIG_FILE           "/config.json"
#define LOG_FILE              "/readings.jsonl"
#define PENDING_FILE          "/pending.jsonl"
#define MAX_LOG_SIZE          (1024 * 1024)    // 1MB max log file
#define MAX_PENDING_SIZE      (512 * 1024)     // 512KB max pending queue
#define NUM_SAMPLES           5                // Ultrasonic samples per reading
#define SOUND_SPEED_CM_US     0.0343           // Speed of sound in cm/µs
#define MAX_DISTANCE_CM       400.0            // HC-SR04 max range
#define PULSE_TIMEOUT_US      23200            // Timeout for 400cm round trip
#define WIFI_CONNECT_TIMEOUT  8000             // 8 seconds to connect WiFi
#define HTTP_TIMEOUT          5000             // 5 seconds for HTTP
#define WATCHDOG_TIMEOUT      30               // 30 second watchdog

// Alert codes
#define CODE_NORMAL           200
#define CODE_WARNING          300
#define CODE_CRITICAL         500
#define CODE_TAMPER           600
#define CODE_SENSOR_ERROR     700

// ============================================================================
//  CONFIGURATION STRUCTURE
// ============================================================================
struct SensorConfig {
  String device_id;
  String device_token;
  float  bin_depth_cm;
  float  min_distance_cm;
  int    warning_threshold;
  int    critical_threshold;
  int    read_interval_minutes;
  String api_endpoint;
  float  registered_lat;
  float  registered_lng;
  String wifi_ssid;
  String wifi_password;
  // WiFi BSSID fingerprint for tamper detection
  String registered_bssid;    // Primary AP BSSID at install location
  int    registered_rssi;     // Expected RSSI (±15 tolerance)
};

SensorConfig config;

// ============================================================================
//  FORWARD DECLARATIONS
// ============================================================================
void loadConfig();
void blinkLED(int times, int delayMs);
float readUltrasonic();
float medianFilter(float* samples, int count);
float calculateFillPercent(float distance);
int   determineAlertCode(float fillPercent, bool tamperDetected);
float readBatteryVoltage();
bool  checkTamper();
void  logReading(float distance, float fillPercent, int alertCode, float batteryV);
bool  sendReading(float distance, float fillPercent, int alertCode, float batteryV);
void  flushPendingReadings();
void  enterDeepSleep();
String getTimestamp();

// ============================================================================
//  BOOT COUNT (persists across deep sleep via RTC memory)
// ============================================================================
RTC_DATA_ATTR int bootCount = 0;

// ============================================================================
//  SETUP — Runs once per wake cycle
// ============================================================================
void setup() {
  // Initialize serial for debug output
  Serial.begin(115200);
  delay(100);
  
  bootCount++;
  Serial.printf("\n\n=== WASTE SENSOR v%s — Boot #%d ===\n", FIRMWARE_VERSION, bootCount);
  
  // Configure watchdog timer (auto-reset if firmware hangs)
  esp_task_wdt_init(WATCHDOG_TIMEOUT, true);
  esp_task_wdt_add(NULL);
  
  // Configure pins
  pinMode(LED_BUILTIN_PIN, OUTPUT);
  pinMode(ULTRASONIC_TRIG_PIN, OUTPUT);
  pinMode(ULTRASONIC_ECHO_PIN, INPUT);
  pinMode(SENSOR_POWER_PIN, OUTPUT);
  pinMode(BATTERY_ADC_PIN, INPUT);
  
  // Startup blink
  blinkLED(2, 100);
  
  // Initialize LittleFS
  if (!LittleFS.begin(true)) {  // true = format on first use
    Serial.println("[ERROR] LittleFS mount failed!");
    blinkLED(10, 50);  // Rapid blink = error
    enterDeepSleep();
    return;
  }
  Serial.println("[OK] LittleFS mounted");
  
  // Load configuration
  loadConfig();
  
  // ---- STEP 1: Power on sensor and take reading ----
  digitalWrite(SENSOR_POWER_PIN, HIGH);
  delay(100);  // Let sensor stabilize
  
  float distance = readUltrasonic();
  
  digitalWrite(SENSOR_POWER_PIN, LOW);  // Power off sensor to save energy
  
  // ---- STEP 2: Calculate fill percentage ----
  float fillPercent = calculateFillPercent(distance);
  
  // ---- STEP 3: Check tamper via WiFi BSSID fingerprint ----
  bool tamperDetected = checkTamper();
  
  // ---- STEP 4: Determine alert code ----
  int alertCode = determineAlertCode(fillPercent, tamperDetected);
  
  // ---- STEP 5: Read battery voltage ----
  float batteryV = readBatteryVoltage();
  
  // ---- STEP 6: Log to LittleFS ----
  logReading(distance, fillPercent, alertCode, batteryV);
  
  // ---- STEP 7: Send if alert or periodic sync ----
  bool shouldSend = (alertCode >= CODE_WARNING) || (bootCount % 4 == 0);  // Send every 4th reading regardless
  
  if (shouldSend) {
    bool sent = sendReading(distance, fillPercent, alertCode, batteryV);
    if (sent) {
      // ---- STEP 8: Flush any buffered offline readings ----
      flushPendingReadings();
      blinkLED(3, 200);  // Success blink
    } else {
      blinkLED(5, 100);  // Fail blink
    }
  }
  
  // ---- STEP 9: Print status summary ----
  Serial.println("\n--- Reading Summary ---");
  Serial.printf("  Distance:    %.1f cm\n", distance);
  Serial.printf("  Fill Level:  %.1f%%\n", fillPercent);
  Serial.printf("  Alert Code:  %d\n", alertCode);
  Serial.printf("  Battery:     %.2f V\n", batteryV);
  Serial.printf("  Tamper:      %s\n", tamperDetected ? "YES!" : "No");
  Serial.printf("  Boot Count:  %d\n", bootCount);
  Serial.println("----------------------\n");
  
  // Reset watchdog before sleep
  esp_task_wdt_reset();
  
  // ---- STEP 10: Deep sleep ----
  enterDeepSleep();
}

void loop() {
  // Never reached — device enters deep sleep after setup()
}

// ============================================================================
//  CONFIGURATION MANAGEMENT
// ============================================================================
void loadConfig() {
  // Set defaults
  config.device_id             = "sensor-001";
  config.device_token          = "dev-token-001";
  config.bin_depth_cm          = 100.0;
  config.min_distance_cm       = 2.0;
  config.warning_threshold     = 70;
  config.critical_threshold    = 90;
  config.read_interval_minutes = 15;
  config.api_endpoint          = "http://localhost:8082/api/v1/iot/readings";
  config.registered_lat        = 6.5244;   // Lagos default
  config.registered_lng        = 3.3792;
  config.wifi_ssid             = "";
  config.wifi_password         = "";
  config.registered_bssid      = "";
  config.registered_rssi       = -60;
  
  // Try to load from file
  File f = LittleFS.open(CONFIG_FILE, "r");
  if (!f) {
    Serial.println("[WARN] No config file found, using defaults");
    return;
  }
  
  JsonDocument doc;
  DeserializationError err = deserializeJson(doc, f);
  f.close();
  
  if (err) {
    Serial.printf("[ERROR] Config parse error: %s\n", err.c_str());
    return;
  }
  
  // Override defaults with file values
  if (doc["device_id"])             config.device_id             = doc["device_id"].as<String>();
  if (doc["device_token"])          config.device_token          = doc["device_token"].as<String>();
  if (doc["bin_depth_cm"])          config.bin_depth_cm          = doc["bin_depth_cm"].as<float>();
  if (doc["min_distance_cm"])       config.min_distance_cm       = doc["min_distance_cm"].as<float>();
  if (doc["warning_threshold"])     config.warning_threshold     = doc["warning_threshold"].as<int>();
  if (doc["critical_threshold"])    config.critical_threshold    = doc["critical_threshold"].as<int>();
  if (doc["read_interval_minutes"]) config.read_interval_minutes = doc["read_interval_minutes"].as<int>();
  if (doc["api_endpoint"])          config.api_endpoint          = doc["api_endpoint"].as<String>();
  if (doc["registered_lat"])        config.registered_lat        = doc["registered_lat"].as<float>();
  if (doc["registered_lng"])        config.registered_lng        = doc["registered_lng"].as<float>();
  if (doc["wifi_ssid"])             config.wifi_ssid             = doc["wifi_ssid"].as<String>();
  if (doc["wifi_password"])         config.wifi_password         = doc["wifi_password"].as<String>();
  if (doc["registered_bssid"])      config.registered_bssid      = doc["registered_bssid"].as<String>();
  if (doc["registered_rssi"])       config.registered_rssi       = doc["registered_rssi"].as<int>();
  
  Serial.printf("[OK] Config loaded — Device: %s, Depth: %.0fcm, Thresholds: %d%%/%d%%\n",
                config.device_id.c_str(), config.bin_depth_cm,
                config.warning_threshold, config.critical_threshold);
}

// ============================================================================
//  ULTRASONIC SENSOR — HC-SR04 with Median Filter
// ============================================================================
float readUltrasonic() {
  float samples[NUM_SAMPLES];
  int validCount = 0;
  
  Serial.println("[SENSOR] Taking ultrasonic readings...");
  
  for (int i = 0; i < NUM_SAMPLES; i++) {
    // Send trigger pulse
    digitalWrite(ULTRASONIC_TRIG_PIN, LOW);
    delayMicroseconds(2);
    digitalWrite(ULTRASONIC_TRIG_PIN, HIGH);
    delayMicroseconds(10);
    digitalWrite(ULTRASONIC_TRIG_PIN, LOW);
    
    // Measure echo pulse duration
    long duration = pulseIn(ULTRASONIC_ECHO_PIN, HIGH, PULSE_TIMEOUT_US);
    
    if (duration > 0) {
      float distance = (duration * SOUND_SPEED_CM_US) / 2.0;
      if (distance >= config.min_distance_cm && distance <= MAX_DISTANCE_CM) {
        samples[validCount++] = distance;
        Serial.printf("  Sample %d: %.1f cm\n", i + 1, distance);
      } else {
        Serial.printf("  Sample %d: %.1f cm (out of range, discarded)\n", i + 1, distance);
      }
    } else {
      Serial.printf("  Sample %d: TIMEOUT (no echo)\n", i + 1);
    }
    
    delay(60);  // HC-SR04 needs ~60ms between measurements
  }
  
  if (validCount == 0) {
    Serial.println("[ERROR] No valid ultrasonic readings!");
    return -1.0;  // Signal sensor error
  }
  
  float result = medianFilter(samples, validCount);
  Serial.printf("[SENSOR] Median distance: %.1f cm (%d valid samples)\n", result, validCount);
  return result;
}

float medianFilter(float* samples, int count) {
  // Simple insertion sort for small array
  for (int i = 1; i < count; i++) {
    float key = samples[i];
    int j = i - 1;
    while (j >= 0 && samples[j] > key) {
      samples[j + 1] = samples[j];
      j--;
    }
    samples[j + 1] = key;
  }
  
  // Return median
  if (count % 2 == 0) {
    return (samples[count / 2 - 1] + samples[count / 2]) / 2.0;
  }
  return samples[count / 2];
}

// ============================================================================
//  FILL CALCULATION
// ============================================================================
float calculateFillPercent(float distance) {
  if (distance < 0) return -1.0;  // Sensor error passthrough
  
  // fill% = ((bin_depth - distance_from_sensor) / bin_depth) * 100
  // distance_from_sensor = distance measured from top of bin to waste surface
  float fill = ((config.bin_depth_cm - distance) / config.bin_depth_cm) * 100.0;
  
  // Clamp to valid range
  fill = fmax(0.0, fmin(100.0, fill));
  
  Serial.printf("[CALC] Distance: %.1f cm → Fill: %.1f%%\n", distance, fill);
  return fill;
}

// ============================================================================
//  ALERT CODE DETERMINATION
// ============================================================================
int determineAlertCode(float fillPercent, bool tamperDetected) {
  if (tamperDetected) {
    Serial.println("[ALERT] CODE 600 — TAMPER DETECTED!");
    return CODE_TAMPER;
  }
  
  if (fillPercent < 0) {
    Serial.println("[ALERT] CODE 700 — SENSOR ERROR!");
    return CODE_SENSOR_ERROR;
  }
  
  if (fillPercent >= config.critical_threshold) {
    Serial.println("[ALERT] CODE 500 — BIN CRITICAL/FULL!");
    return CODE_CRITICAL;
  }
  
  if (fillPercent >= config.warning_threshold) {
    Serial.println("[ALERT] CODE 300 — BIN WARNING!");
    return CODE_WARNING;
  }
  
  Serial.println("[INFO] CODE 200 — Normal");
  return CODE_NORMAL;
}

// ============================================================================
//  BATTERY VOLTAGE READING
// ============================================================================
float readBatteryVoltage() {
  // ESP32-C3 ADC: 12-bit (0-4095), reference ~2.5V
  // Voltage divider: 100K + 100K → Vmeasured = Vbat / 2
  // So Vbat = ADC_reading * (2.5 / 4095) * 2
  
  int raw = 0;
  for (int i = 0; i < 10; i++) {
    raw += analogRead(BATTERY_ADC_PIN);
    delay(2);
  }
  raw /= 10;  // Average 10 samples for stability
  
  float voltage = (raw / 4095.0) * 2.5 * 2.0;  // Through voltage divider
  
  // Calibration offset (adjust per unit if needed)
  voltage += 0.1;
  
  Serial.printf("[BATTERY] ADC: %d → %.2f V\n", raw, voltage);
  return voltage;
}

// ============================================================================
//  TAMPER DETECTION via WiFi BSSID Fingerprint
// ============================================================================
bool checkTamper() {
  if (config.registered_bssid.length() == 0) {
    Serial.println("[TAMPER] No registered BSSID — skipping tamper check");
    return false;  // Not configured, skip check
  }
  
  Serial.println("[TAMPER] Scanning WiFi networks for location verification...");
  
  int n = WiFi.scanNetworks(false, true, false, 300);  // Passive scan, 300ms per channel
  
  if (n <= 0) {
    Serial.println("[TAMPER] No networks found — possible tampering or shielding");
    WiFi.scanDelete();
    return true;  // Conservative: if we can't see ANY networks, something is wrong
  }
  
  bool foundRegistered = false;
  for (int i = 0; i < n; i++) {
    String bssid = WiFi.BSSIDstr(i);
    int rssi = WiFi.RSSI(i);
    
    if (bssid.equalsIgnoreCase(config.registered_bssid)) {
      foundRegistered = true;
      // Check RSSI within tolerance (±20 dBm)
      if (abs(rssi - config.registered_rssi) > 20) {
        Serial.printf("[TAMPER] Registered AP found but RSSI anomaly: expected %d, got %d\n",
                      config.registered_rssi, rssi);
        // RSSI anomaly alone is not enough to flag tamper
      }
      Serial.printf("[TAMPER] Registered AP found: %s (RSSI: %d) — Location OK\n",
                    bssid.c_str(), rssi);
      break;
    }
  }
  
  WiFi.scanDelete();
  
  if (!foundRegistered) {
    Serial.println("[TAMPER] Registered AP NOT found — device may have been moved!");
    return true;
  }
  
  return false;
}

// ============================================================================
//  DATA LOGGING to LittleFS
// ============================================================================
void logReading(float distance, float fillPercent, int alertCode, float batteryV) {
  // Check file size, rotate if needed
  if (LittleFS.exists(LOG_FILE)) {
    File check = LittleFS.open(LOG_FILE, "r");
    if (check && check.size() > MAX_LOG_SIZE) {
      check.close();
      LittleFS.remove("/readings_old.jsonl");
      LittleFS.rename(LOG_FILE, "/readings_old.jsonl");
      Serial.println("[LOG] Rotated log file");
    } else {
      check.close();
    }
  }
  
  // Build JSON record
  JsonDocument doc;
  doc["device_id"]    = config.device_id;
  doc["timestamp"]    = getTimestamp();
  doc["distance_cm"]  = round(distance * 10) / 10.0;
  doc["fill_percent"] = round(fillPercent * 10) / 10.0;
  doc["alert_code"]   = alertCode;
  doc["battery_v"]    = round(batteryV * 100) / 100.0;
  doc["boot"]         = bootCount;
  
  String jsonLine;
  serializeJson(doc, jsonLine);
  
  // Append to log file
  File f = LittleFS.open(LOG_FILE, "a");
  if (f) {
    f.println(jsonLine);
    f.close();
    Serial.printf("[LOG] Saved: %s\n", jsonLine.c_str());
  } else {
    Serial.println("[ERROR] Failed to write log file!");
  }
  
  // Also append to pending queue (for network sync)
  File p = LittleFS.open(PENDING_FILE, "a");
  if (p) {
    p.println(jsonLine);
    p.close();
  }
}

// ============================================================================
//  NETWORK COMMUNICATION — WiFi HTTP POST
// ============================================================================
bool sendReading(float distance, float fillPercent, int alertCode, float batteryV) {
  if (config.wifi_ssid.length() == 0) {
    Serial.println("[NET] No WiFi configured — reading buffered offline");
    return false;
  }
  
  Serial.printf("[NET] Connecting to WiFi: %s\n", config.wifi_ssid.c_str());
  
  WiFi.mode(WIFI_STA);
  WiFi.begin(config.wifi_ssid.c_str(), config.wifi_password.c_str());
  
  unsigned long startMs = millis();
  while (WiFi.status() != WL_CONNECTED && (millis() - startMs) < WIFI_CONNECT_TIMEOUT) {
    delay(100);
    esp_task_wdt_reset();
  }
  
  if (WiFi.status() != WL_CONNECTED) {
    Serial.println("[NET] WiFi connection failed — reading buffered offline");
    WiFi.disconnect(true);
    WiFi.mode(WIFI_OFF);
    return false;
  }
  
  Serial.printf("[NET] Connected! IP: %s\n", WiFi.localIP().toString().c_str());
  
  // Build API payload
  JsonDocument doc;
  doc["device_id"]    = config.device_id;
  doc["measured_at"]  = getTimestamp();
  doc["distance_cm"]  = round(distance * 100) / 100.0;
  doc["fill_percent"] = round(fillPercent * 100) / 100.0;
  doc["alert_code"]   = alertCode;
  doc["latitude"]     = config.registered_lat;
  doc["longitude"]    = config.registered_lng;
  doc["battery_voltage"] = round(batteryV * 100) / 100.0;
  doc["is_simulated"] = false;
  doc["firmware"]     = FIRMWARE_VERSION;
  
  String payload;
  serializeJson(doc, payload);
  
  // Send HTTP POST
  HTTPClient http;
  http.begin(config.api_endpoint);
  http.addHeader("Content-Type", "application/json");
  http.addHeader("X-Device-ID", config.device_id);
  http.addHeader("X-Device-Token", config.device_token);
  http.setTimeout(HTTP_TIMEOUT);
  
  int httpCode = http.POST(payload);
  
  bool success = false;
  if (httpCode > 0) {
    String response = http.getString();
    Serial.printf("[NET] HTTP %d: %s\n", httpCode, response.c_str());
    success = (httpCode >= 200 && httpCode < 300);
  } else {
    Serial.printf("[NET] HTTP error: %s\n", http.errorToString(httpCode).c_str());
  }
  
  http.end();
  
  if (!success) {
    Serial.println("[NET] Send failed — reading remains in pending queue");
  }
  
  return success;
}

// ============================================================================
//  FLUSH PENDING OFFLINE READINGS
// ============================================================================
void flushPendingReadings() {
  if (!LittleFS.exists(PENDING_FILE)) return;
  
  File f = LittleFS.open(PENDING_FILE, "r");
  if (!f || f.size() == 0) {
    f.close();
    return;
  }
  
  Serial.printf("[SYNC] Flushing %d bytes of pending readings...\n", f.size());
  
  int sent = 0;
  int failed = 0;
  String pendingLines = "";  // Lines that fail to send
  
  while (f.available()) {
    String line = f.readStringUntil('\n');
    line.trim();
    if (line.length() == 0) continue;
    
    // Send each buffered reading
    HTTPClient http;
    http.begin(config.api_endpoint);
    http.addHeader("Content-Type", "application/json");
    http.addHeader("X-Device-ID", config.device_id);
    http.addHeader("X-Device-Token", config.device_token);
    http.setTimeout(HTTP_TIMEOUT);
    
    int httpCode = http.POST(line);
    http.end();
    
    if (httpCode >= 200 && httpCode < 300) {
      sent++;
    } else {
      failed++;
      pendingLines += line + "\n";
    }
    
    esp_task_wdt_reset();
    delay(100);  // Don't hammer the server
  }
  
  f.close();
  
  // Rewrite pending file with only the failed ones
  File p = LittleFS.open(PENDING_FILE, "w");
  if (p) {
    if (pendingLines.length() > 0) {
      p.print(pendingLines);
    }
    p.close();
  }
  
  Serial.printf("[SYNC] Flushed: %d sent, %d still pending\n", sent, failed);
  
  // Disconnect WiFi to save power
  WiFi.disconnect(true);
  WiFi.mode(WIFI_OFF);
}

// ============================================================================
//  DEEP SLEEP
// ============================================================================
void enterDeepSleep() {
  uint64_t sleepUs = (uint64_t)config.read_interval_minutes * 60ULL * 1000000ULL;
  
  Serial.printf("[SLEEP] Entering deep sleep for %d minutes...\n\n", config.read_interval_minutes);
  Serial.flush();
  
  // Turn off everything
  digitalWrite(LED_BUILTIN_PIN, LOW);
  digitalWrite(SENSOR_POWER_PIN, LOW);
  WiFi.disconnect(true);
  WiFi.mode(WIFI_OFF);
  
  esp_task_wdt_delete(NULL);
  esp_sleep_enable_timer_wakeup(sleepUs);
  esp_deep_sleep_start();
}

// ============================================================================
//  UTILITIES
// ============================================================================
void blinkLED(int times, int delayMs) {
  for (int i = 0; i < times; i++) {
    digitalWrite(LED_BUILTIN_PIN, HIGH);
    delay(delayMs);
    digitalWrite(LED_BUILTIN_PIN, LOW);
    delay(delayMs);
  }
}

String getTimestamp() {
  // Without RTC or NTP, use boot count + millis as pseudo-timestamp
  // In production with WiFi, sync NTP time on each connection
  
  if (WiFi.status() == WL_CONNECTED) {
    configTime(3600, 0, "pool.ntp.org", "time.nist.gov");  // WAT = UTC+1
    struct tm timeinfo;
    if (getLocalTime(&timeinfo, 5000)) {
      char buf[30];
      strftime(buf, sizeof(buf), "%Y-%m-%dT%H:%M:%S+01:00", &timeinfo);
      return String(buf);
    }
  }
  
  // Fallback: boot-relative timestamp
  char buf[32];
  snprintf(buf, sizeof(buf), "boot%d-t%lu", bootCount, millis());
  return String(buf);
}
