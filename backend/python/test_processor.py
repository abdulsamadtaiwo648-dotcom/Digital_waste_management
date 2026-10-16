import unittest
from processor import calculate_fill


class FillCalculationTests(unittest.TestCase):
    def test_half_full(self):
        self.assertEqual(calculate_fill(50, 100, 5, 0), {"fill_percent": 50.0, "level": "normal"})

    def test_threshold_boundaries(self):
        self.assertEqual(calculate_fill(30, 100, 5, 0)["level"], "warning")
        self.assertEqual(calculate_fill(10, 100, 5, 0)["level"], "critical")

    def test_rejects_measurement_below_sensor_minimum(self):
        with self.assertRaises(ValueError):
            calculate_fill(3, 100, 5, 0)

    def test_uses_configured_thresholds(self):
        self.assertEqual(calculate_fill(25, 100, 5, 0, 60, 85)["level"], "warning")


if __name__ == "__main__":
    unittest.main()
