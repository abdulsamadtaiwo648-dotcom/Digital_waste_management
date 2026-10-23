# 🗑️ Waste Bin Level Sensor — Hardware & Firmware Design

> Part of the **Digital Waste Management System**

Smart IoT sensor node that monitors waste bin fill levels, detects tampering, logs readings offline, and sends alerts to the backend system.

---

## 📐 Architecture Overview

```
┌──────────────────────────────────────────────┐
│             WASTE BIN (with lid)             │
│                                              │
│  ┌────────────────────────────────────┐      │
│  │   ESP32-C3 SuperMini               │      │
│  │   + HC-SR04 Ultrasonic             │──────│──→ WiFi / Hotspot → Backend API
│  │   + TP4056 + 18650 Li-ion Battery  │      │
│  └────────────────────────────────────┘      │
│                 ↓ (ultrasonic pulse)         │
│  ░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░  │ ← Waste surface
│  ████████████████████████████████████████████│ ← Waste
└──────────────────────────────────────────────┘
```

---

## 🛒 Bill of Materials (BOM) & Target Cost (< ₦5,000)

### Option A: Volume / Batch Sourcing (Direct Maker Import to Nigeria) — **~₦5,390 (or ~₦4,510 with ESP-01S)**

| Component | Part Description | Approx Price (₦) |
|-----------|------------------|------------------|
| **Microcontroller** | ESP32-C3 SuperMini (RISC-V, WiFi 4 + BLE 5, USB-C, 22.5×18mm) | ₦2,240 |
| **Ultrasonic Sensor** | HC-SR04 / HC-SR04P (2cm – 400cm range) | ₦1,040 |
| **Battery Charger** | TP4056 1A Li-ion board with DW01 protection | ₦400 |
| **Battery Holder** | 18650 single-cell clip with leads | ₦320 |
| **Battery** | Reclaimed/tested Grade-A 18650 cell (2000mAh, Computer Village/local) | ₦1,000 |
| **Passives & Wiring** | Voltage divider resistors (100kΩ/100kΩ & 1kΩ/2kΩ), wires | ₦240 |
| **Waterproofing** | Conformal coating / clear lacquer + polyethylene membrane | ₦150 |
| **TOTAL** | | **~₦5,390** |

> *Note:* If swapped with **ESP8266 ESP-01S**, the total drops to **₦4,510**, safely under ₦5,000. However, the ESP32-C3 SuperMini offers superior deep sleep (~43µA), 4MB flash for offline LittleFS logging, USB-C flashing, and BLE provisioning.

### Option B: Local Retail (Lagos — Hub360 / Nerdshed)

| Component | Sourced From | Local Shelf Price (₦) |
|-----------|--------------|-----------------------|
| **Microcontroller** | ESP-01S (₦4,400) or ESP32-C3 SuperMini (₦6,450) | ₦4,400 – ₦6,450 |
| **Ultrasonic Sensor** | HC-SR04 Module | ₦2,150 |
| **Battery Charger** | TP4056 Charging Board with Protection | ₦460 |
| **Battery** | Reclaimed 18650 Li-ion 3.7V Cell | ₦1,500 |
| **Battery Holder** | 18650 Single Slot with Leads | ₦400 |
| **Passives & Wires** | Resistors + hookup wire | ₦200 |
| **TOTAL** | | **₦9,110 – ₦11,160** |

---

## 🔌 Wiring & Pinout Guide

```
                    ESP32-C3 SuperMini
                   ┌─────────────────┐
                   │    [USB-C]      │
          ┌────────┤ 3V3         GND ├────────┐
          │        │                 │        │
          │   ┌────┤ GPIO2      GPIO0├──┐     │
          │   │    │                 │  │     │
          │   │ ┌──┤ GPIO3      GPIO8├──│──┐  │
          │   │ │  │                 │  │  │  │
          │   │ │  │ GPIO4           │  │  │  │
          │   │ │  └────┬────────────┘  │  │  │
          │   │ │       │               │  │  │
          │   │ │       │ P-MOSFET      │  │  │
          │   │ │       │ Gate          │  │  │
     ┌────┘   │ │   ┌───┴───┐          │  │  │
     │        │ │   │       │          │  │  │
     │ VCC────│─│───┤S    D ├───VCC    │  │  │
     │        │ │   └───────┘  HC-SR04 │  │  │
     │        │ │              ┌──────┐│  │  │
     │ GND    │ └──────────────┤ECHO  ││  │  │
     │  │     └────────────────┤TRIG  ││  │  │
     │  │                      │VCC   ││  │  │
     │  │                      │GND───┘│  │  │
     │  │                      └──────┘│  │  │
     │  │                              │  │  │
     │  │    Battery Voltage Divider   │  │  │
     │  │    ┌─── 100kΩ ──┬── 100kΩ ───┘  │  │
     │  │    │             │              │  │
     │  │    │          GPIO0/ADC         │  │
     │  │    │                            │  │
     │  │  BAT+                        LED│  │
     │  │    │    TP4056 Module         (Built-in)
     │  │  ┌─┴─────────────┐              │
     │  └──┤ B-    B+      │              │
     │     │               │              │
     │     │ OUT+    OUT-  │              │
     │     └─┬────────┬────┘              │
     │       │        │                   │
     │    To ESP32   GND ─────────────────┘
     │    3V3/VIN
     │
   18650 Battery (3.7V, 2000mAh)
```

### Pin Assignments

| Pin | Function | Description |
|-----|----------|-------------|
| **GPIO0** | ADC Input | Battery level monitor via 100kΩ/100kΩ divider |
| **GPIO2** | Digital Output | HC-SR04 Ultrasonic Trigger pulse |
| **GPIO3** | Digital Input | HC-SR04 Ultrasonic Echo return |
| **GPIO4** | Digital Output | High-side power switch (P-MOSFET gate) to cut sensor power during sleep |
| **GPIO8** | Digital Output | Status LED (built-in) |

---

## 📡 Alert Codes & Workflow

| Code | Level | Condition | Action |
|------|-------|-----------|--------|
| `200` | **Normal** | Fill level < 70% | Logged to LittleFS; synced on scheduled upload window |
| `300` | **Warning** (Yellow) | Fill level ≥ 70% | Alert triggered; prioritizes network uplink |
| `500` | **Critical / Full** (Red) | Fill level ≥ 90% | Urgent alert triggered; dispatch collection |
| `600` | **Tamper / Stolen** | Device moved > 50m / unexpected BSSID | Immediate security flag sent to operations dashboard |
| `700` | **Sensor Error** | Measurement out-of-range or timeout | Maintenance required |

### Measurement Cycle (Duty Cycle):
1. **Wake up** from deep sleep timer (every 15–30 minutes).
2. **Power up sensor** via GPIO4 P-MOSFET gate.
3. **Sample 5 readings** with median filtering to eliminate bounce/trash bag noise.
4. **Calculate fill percentage**: `fill% = ((bin_depth - distance) / bin_depth) * 100`.
5. **Verify location** via WiFi BSSID scanning against initial provisioned footprint.
6. **Log locally** to LittleFS flash memory (JSON Lines format).
7. **If Alert Code ≥ 300** or upload window reached, connect to WiFi and send payload. Flush buffered pending readings on success.
8. **Cut sensor power** and enter deep sleep (~43–100µA draw).

---

## 💡 Geolocation & Connectivity: Safe, Cheap & Efficient

1. **Why not Hardware GPS (e.g. NEO-6M)?**
   - GPS modules cost ₦7,750–₦14,000 (exceeds budget by itself).
   - Draw 50–70mA continuously for 30–60 seconds to lock satellites.
   - Metal/plastic bin lids and urban alleys block satellite signals.
   - **Solution:** **Static Asset Geotagging**. During installation, staff scan a QR code on the bin lid using a mobile phone app. The phone's GPS captures the precise coordinate (sub-meter) and tags the device in the backend database.
   - **Tamper Detection (Code 600):** The node performs a 200ms passive WiFi scan. If the nearby Access Point BSSID signature deviates or disappears while in motion, Code 600 is raised.

2. **Why not eSIM / SIM800L for cellular?**
   - eSIM chips and cellular modules (SIM800L / NB-IoT) cost ₦8,500–₦25,000 plus ongoing monthly airtime/data costs.
   - 2A current bursts cause brownouts on small batteries.
   - **Solution:** 
     - **Option 1 (Fixed Urban):** Municipal/Commercial/Residential WiFi where bins are placed.
     - **Option 2 (Drive-By Truck Hotspot):** Waste collection trucks broadcast an SSID (e.g., `WasteTruckNet`). When the truck pulls up to empty the bin, the sensor connects in 2 seconds, flushes all LittleFS logs, and sleeps.

---

## 🔋 Power Budget & Lifespan

- **Sleep current:** ~100 µA (23.96 hours/day) = **2.4 mAh/day**
- **Active reading (3 seconds @ 95 mA, 48x/day):** **3.8 mAh/day**
- **Battery self-discharge allowance:** **1.8 mAh/day**
- **Total daily consumption:** **~8.0 mAh/day**
- **Lifespan on 2000mAh 18650 cell (80% depth of discharge = 1600mAh):**
  $$\frac{1600 \text{ mAh}}{8.0 \text{ mAh/day}} \approx \mathbf{200 \text{ Days (~6.5 Months per charge)}}$$

Rechargeable via standard USB-C on the TP4056 board in ~2 hours.

---

## 🖥️ Presentation Simulator

A Python-based simulator is included for demonstrations, testing, and presentations. It runs directly with Python standard library (no pip dependencies required).

### Run the Simulator

```powershell
cd "sensor\simulator"

# Run default presentation demo (cycles through normal, rapid, theft, and intermittent modes)
python sensor_simulator.py

# Run specific scenarios
python sensor_simulator.py --mode normal
python sensor_simulator.py --mode rapid
python sensor_simulator.py --mode theft
python sensor_simulator.py --mode intermittent

# Test with live backend integration
python sensor_simulator.py --device-id sensor-001
```

---

## 🛠️ Firmware Build & Flash (PlatformIO)

```powershell
cd "sensor\firmware"

# Compile firmware
pio run

# Flash to ESP32-C3 via USB-C
pio run --target upload

# Upload LittleFS configuration (data/config.json)
pio run --target uploadfs

# Monitor serial output
pio device monitor
```
