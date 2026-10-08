package obdcollector

import (
	"fmt"
	"math"
	"strings"
)

var bounds = map[string]struct{ min, max float64 }{
	"rpm":                         {0, 20000},
	"speed_kph":                   {0, 300},
	"coolant_c":                   {-40, 215},
	"intake_c":                    {-40, 215},
	"load_pct":                    {0, 100},
	"throttle_pct":                {0, 100},
	"maf_gps":                     {0, 655.35},
	"manifold_kpa":                {0, 255},
	"fuel_level_pct":              {0, 100},
	"short_fuel_trim_pct":         {-100, 100},
	"long_fuel_trim_pct":          {-100, 100},
	"timing_advance_deg":          {-64, 63.5},
	"run_time_s":                  {0, 65535},
	"control_voltage_v":           {0, 65.535},
	"adapter_voltage_v":           {0, 40},
	"mil_on":                      {0, 1},
	"dtc_count":                   {0, 127},
	"distance_since_clear_km":     {0, 65535},
	"warmups_since_clear":         {0, 255},
	"distance_with_mil_km":        {0, 65535},
	"fuel_pressure_kpa":           {0, 765},
	"short_fuel_trim_bank2_pct":   {-100, 99.21875},
	"long_fuel_trim_bank2_pct":    {-100, 99.21875},
	"fuel_rail_pressure_kpa":      {0, 5177.265},
	"fuel_rail_gauge_kpa":         {0, 655350},
	"egr_commanded_pct":           {0, 100},
	"egr_error_pct":               {-100, 99.21875},
	"evap_purge_pct":              {0, 100},
	"evap_pressure_pa":            {-8192, 8191.75},
	"barometric_kpa":              {0, 255},
	"catalyst_b1s1_c":             {-40, 6513.5},
	"catalyst_b2s1_c":             {-40, 6513.5},
	"catalyst_b1s2_c":             {-40, 6513.5},
	"catalyst_b2s2_c":             {-40, 6513.5},
	"absolute_load_pct":           {0, 25700},
	"commanded_equivalence_ratio": {0, 1.999969482421875},
	"relative_throttle_pct":       {0, 100},
	"ambient_c":                   {-40, 215},
	"throttle_b_pct":              {0, 100},
	"throttle_c_pct":              {0, 100},
	"pedal_d_pct":                 {0, 100},
	"pedal_e_pct":                 {0, 100},
	"pedal_f_pct":                 {0, 100},
	"commanded_throttle_pct":      {0, 100},
	"time_with_mil_min":           {0, 65535},
	"time_since_clear_min":        {0, 65535},
	"ethanol_pct":                 {0, 100},
	"evap_absolute_kpa":           {0, 327.675},
	"evap_pressure_wide_pa":       {-32767, 32768},
	"fuel_rail_absolute_kpa":      {0, 655350},
	"relative_pedal_pct":          {0, 100},
	"oil_c":                       {-40, 215},
	"fuel_injection_deg":          {-210, 301.9921875},
	"fuel_rate_lph":               {0, 3276.75},
	"fuel_system_1_status":        {1, 16},
	"fuel_system_2_status":        {1, 16},
	"readiness_supported_count":   {0, 11},
	"readiness_incomplete_count":  {0, 11},
	"ignition_compression":        {0, 1},
	"adapter_engine_start_count":  {0, 4294967295},
	"adapter_power_on_count":      {0, 4294967295},
	"adapter_uptime_s":            {0, 4294967295},
}

func init() {
	for i := 1; i <= 8; i++ {
		bounds[fmt.Sprintf("o2_sensor_%d_v", i)] = struct{ min, max float64 }{0, 1.275}
		bounds[fmt.Sprintf("o2_sensor_%d_trim_pct", i)] = struct{ min, max float64 }{-100, 98.4375}
	}
}

// Preserve the existing reader's physical bounds independently of presentation units.
func validReading(metric string, value float64) bool {
	if strings.HasPrefix(metric, "readiness_") && strings.HasSuffix(metric, "_ready") {
		return value == 0 || value == 1
	}
	b, ok := bounds[metric]
	return ok && !math.IsNaN(value) && !math.IsInf(value, 0) && value >= b.min && value <= b.max
}

// ValidLegacyReading retains the receiver's discrete and physical validation contract.
func ValidLegacyReading(name string, value float64) bool {
	if !validReading(name, value) {
		return false
	}
	if name == "fuel_system_1_status" || name == "fuel_system_2_status" {
		return value == 1 || value == 2 || value == 4 || value == 8 || value == 16
	}
	if name == "mil_on" || name == "dtc_count" || name == "ignition_compression" || name == "warmups_since_clear" || name == "adapter_engine_start_count" || name == "adapter_power_on_count" || name == "adapter_uptime_s" || strings.HasPrefix(name, "readiness_") {
		return math.Trunc(value) == value
	}
	return true
}
