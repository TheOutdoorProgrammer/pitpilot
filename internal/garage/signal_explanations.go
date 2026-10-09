package garage

import "strings"

type signalExplanation struct{ description, interpretation string }

var signalExplanations = map[string]signalExplanation{
	"manifold_kpa":                {"Air pressure inside the engine's intake manifold, measured relative to a vacuum.", "This is intake air pressure, not tire or fuel pressure. Compare it with barometric pressure and engine load. It changes with throttle, altitude and engine design; one reading does not diagnose a fault."},
	"barometric_kpa":              {"Atmospheric air pressure reported by the vehicle.", "Weather and altitude affect this value. It provides context for intake manifold pressure; absolute pressure uses a vacuum as its reference."},
	"fuel_pressure_kpa":           {"Fuel supply pressure reported by the standard low-range OBD fuel-pressure channel.", "This channel uses gauge pressure. Compare with the specification for this engine and fuel system, not an absolute or manifold-relative pressure channel."},
	"fuel_rail_pressure_kpa":      {"Fuel rail pressure measured relative to intake manifold pressure.", "The reference matters: this is a different measurement from atmospheric gauge pressure or absolute rail pressure. Use the matching manufacturer specification."},
	"fuel_rail_gauge_kpa":         {"Fuel rail pressure measured relative to atmospheric pressure.", "Direct-injection and diesel systems can report very different pressures from port-injection engines. Compare only with this engine's specification."},
	"fuel_rail_absolute_kpa":      {"Fuel rail pressure measured relative to a vacuum.", "Absolute pressure includes atmospheric pressure. Do not compare it directly with a gauge-pressure specification."},
	"evap_pressure_pa":            {"Pressure difference reported by the fuel-vapor evaporative system.", "A negative value indicates pressure below this channel's reference. The reading changes during vapor-system operation; it is separate from fuel rail pressure."},
	"evap_pressure_wide_pa":       {"Fuel-vapor system pressure from the wider-range OBD pressure channel.", "Keep this channel separate from the other evaporative pressure channels. Compare the same sensor and operating conditions over time."},
	"evap_absolute_kpa":           {"Absolute pressure inside the fuel-vapor evaporative system.", "Its vacuum reference differs from the relative evaporative pressure channels. This is vapor-system pressure, not the pressure supplying the injectors."},
	"coolant_c":                   {"Temperature of the engine coolant reported by the vehicle.", "Follow the change from a cold start to a warmed engine. Normal operating temperature depends on the engine and its cooling controls; the dashboard warning system remains the immediate reference."},
	"intake_c":                    {"Temperature of the air at the engine's intake-air sensor.", "It can differ from outdoor temperature because of sensor placement and heat under the hood. Compare similar driving and engine-temperature conditions."},
	"ambient_c":                   {"Outside-air temperature reported by the vehicle.", "Parking, nearby heat and sensor placement can affect a recent reading. This is distinct from air inside the intake manifold."},
	"oil_c":                       {"Engine oil temperature reported by the vehicle.", "Oil and coolant warm at different rates. Compare with the engine manufacturer's operating guidance, rather than treating coolant temperature as an oil-temperature target."},
	"rpm":                         {"Engine crankshaft speed in revolutions per minute.", "Use this to distinguish stopped, idling and running conditions alongside vehicle speed. Idle speed varies with temperature, accessories and engine controls."},
	"speed_kph":                   {"Vehicle speed reported at the observation time.", "This is a speed reading, not an odometer reading. Sparse samples can miss brief changes; the source and timestamp show where and when it was measured."},
	"daily_top_speed_kph":         {"Highest speed retained in the imported reporting period.", "A daily summary is not a continuous recording and does not establish the exact time or location of that maximum."},
	"load_pct":                    {"The engine controller's calculated load percentage.", "It describes how hard the engine is working under its current conditions. It is not accelerator position, horsepower or a percentage of fuel remaining."},
	"absolute_load_pct":           {"The engine controller's absolute-load calculation from air charge.", "This differs from calculated load and may exceed 100 percent on some engines. Compare this same metric across similar operating conditions."},
	"throttle_pct":                {"Position reported by the primary throttle-position channel.", "The throttle controls incoming air. Its reported percentage is not necessarily pedal position, and a closed throttle need not read exactly zero."},
	"relative_throttle_pct":       {"Throttle position expressed relative to the controller's learned reference.", "This uses a different reference from absolute throttle position. Compare the same channel over time instead of treating the two as interchangeable."},
	"commanded_throttle_pct":      {"Throttle position requested by the engine controller.", "A command is a target, not confirmation of the actual valve position. Compare with a measured throttle channel when both are available."},
	"relative_pedal_pct":          {"Accelerator pedal position relative to its calibrated reference.", "This indicates driver input. Electronic throttle controls may request a different throttle opening from that pedal position."},
	"timing_advance_deg":          {"Ignition timing advance reported for the reference cylinder.", "The controller changes spark timing with speed, load, temperature and other conditions. This is an angle, not an instruction to adjust mechanical timing."},
	"fuel_injection_deg":          {"Fuel-injection timing angle reported by the engine controller.", "This is separate from spark timing. Its useful range and reference depend on the engine and injection system."},
	"maf_gps":                     {"Mass of air passing the airflow sensor each second.", "The unit is grams per second, not GPS location. Airflow changes with engine speed, size and load; compare similar conditions."},
	"fuel_rate_lph":               {"Fuel consumption rate reported in liters per hour.", "This measures fuel per time, not miles per gallon. Idling can consume fuel while covering no distance."},
	"fuel_level_pct":              {"Estimated portion of the fuel tank remaining, expressed as a percentage.", "100 percent represents full and 0 percent represents empty on the source's scale. Tank shape, sloshing and reporting delays can make readings change unevenly."},
	"fuel_remaining_l":            {"Estimated fuel remaining in the tank, expressed in liters.", "This is a volume estimate, not fuel used during a trip. Use its timestamp and the dashboard gauge when planning a refill."},
	"oil_life_pct":                {"The vehicle's estimate of remaining engine-oil service life.", "This is a maintenance estimate, not the amount of oil in the engine. It does not replace an oil-level check or the manufacturer's service instructions."},
	"ethanol_pct":                 {"Ethanol content of the fuel as reported by the vehicle.", "This describes fuel composition, not tank fullness or fuel trim. Availability and how it is determined depend on the vehicle."},
	"commanded_equivalence_ratio": {"The controller's requested fuel-and-air mixture ratio on the OBD equivalence scale.", "A value of 1 is the stoichiometric reference. This is a unitless command, not a measured air-to-fuel ratio or an oxygen-sensor voltage."},
	"egr_commanded_pct":           {"Requested exhaust-gas recirculation reported by the controller.", "EGR routes some exhaust back through the intake. This is a command; it does not by itself confirm actual valve position or gas flow."},
	"egr_error_pct":               {"Reported difference between commanded and actual exhaust-gas recirculation on the controller's percentage scale.", "Interpret the sign and operating conditions with the engine's diagnostic information. A single transient value is not a component diagnosis."},
	"evap_purge_pct":              {"Requested operation of the evaporative-system purge control.", "Purge draws captured fuel vapor into the engine. This is a control percentage, not tank pressure or the amount of vapor flowing."},
	"adapter_voltage_v":           {"Supply voltage measured by the diagnostic adapter at its vehicle connection.", "This can help show power and charging changes. It is not a battery state-of-charge percentage, and adapter voltage may differ from a measurement directly at the battery."},
	"control_voltage_v":           {"Supply voltage reported by the vehicle's control module.", "Compare this with adapter voltage only with their different measurement points in mind. A voltage sample alone does not establish battery health."},
	"adapter_uptime_s":            {"Time reported by the adapter for its current uptime counter.", "An adapter reset or power cycle can reset this value. It is not necessarily the length of a drive or the engine's running time."},
	"adapter_engine_start_count":  {"Engine-start counter reported by the diagnostic adapter.", "Treat this as an adapter counter, not a verified trip count. Its reset behavior and detection depend on the adapter."},
	"adapter_power_on_count":      {"Power-on counter reported by the diagnostic adapter.", "This reflects the adapter's power history. It does not establish how many times the vehicle was driven."},
	"run_time_s":                  {"Engine running time since the current engine start, as reported by the vehicle.", "The counter can restart with a new engine start. It is separate from a daily total or adapter uptime."},
	"running_time_s":              {"Engine-running duration retained in a reporting period.", "This is a summary of captured data. Missing collection intervals mean it may not cover the entire time the engine was running."},
	"moving_time_s":               {"Time classified as moving in the captured reporting period.", "This depends on the source's movement rule and available samples. It is not necessarily the full elapsed duration of a trip."},
	"idle_time_s":                 {"Time classified as idling in the captured reporting period.", "This represents engine-running periods without movement according to the source. Collection gaps can make it incomplete."},
	"speed_coverage_s":            {"Duration covered by usable speed samples in the reporting period.", "Coverage describes the evidence behind a distance estimate. It is not additional driving time to add to running or moving time."},
	"retained_samples":            {"Number of readings retained for the source's reporting period.", "This helps judge how much evidence a summary contains. More samples do not guarantee that every part of the drive was captured."},
	"skipped_intervals":           {"Number of collection intervals the source could not use.", "Gaps reduce coverage. PitPilot does not fill missing measurements with zero or invent a route through them."},
	"distance_km":                 {"Distance estimate supplied by an imported reporting period.", "This belongs to that summary and is not an absolute odometer. Old summary estimates are not automatically added again to a measured odometer."},
	"driving_distance_km":         {"Distance derived from adjacent captured vehicle-speed samples.", "PitPilot adds eligible, nonoverlapping intervals after the latest odometer baseline. Gaps, restarts and invalid timing do not create invented mileage."},
	"odometer_km":                 {"Total vehicle distance reported by the source.", "This is an absolute reading, not extra distance to add. A newer measured reading can recalibrate PitPilot's mileage baseline."},
	"range_km":                    {"Remaining driving range estimated by the vehicle or provider.", "Driving conditions and energy use can change this estimate. It is not distance already traveled or a guaranteed distance before refueling."},
	"mil_on":                      {"Whether the vehicle reports its malfunction indicator, commonly called the check-engine light, as on.", "On and off are states, not magnitudes. An off value does not prove there are no pending faults; inspect available diagnostic codes for more context."},
	"ignition_compression":        {"Whether the vehicle reports the compression-ignition readiness-monitor family.", "On identifies compression-ignition monitoring; off identifies spark-ignition monitoring. This is not an indication that the ignition or engine is currently on."},
	"dtc_count":                   {"Number of diagnostic trouble codes reported by this status reading.", "This is a count, not the codes themselves or a diagnosis. Read the accompanying diagnostic context to see which codes were actually available."},
	"readiness_incomplete_count":  {"Number of supported emissions readiness checks reported as incomplete.", "Incomplete means the checks have not completed under the required conditions. It does not by itself mean those systems have failed."},
	"readiness_supported_count":   {"Number of emissions readiness checks supported by the reported vehicle data.", "Unsupported checks are not failed checks. Compare completion against the supported set for this vehicle."},
	"warmups_since_clear":         {"Warm-up cycles counted by the vehicle since diagnostic information was cleared.", "This is the controller's warm-up count, not a count of trips. Clearing diagnostic information can restart it."},
	"time_since_clear_min":        {"Time counter reported since diagnostic trouble codes were cleared.", "Treat this as the vehicle's diagnostic counter, not proof of a particular wall-clock clearing time."},
	"time_with_mil_min":           {"Engine-running time accumulated while the malfunction indicator was active.", "This diagnostic counter is not necessarily how long the light has been on in wall-clock time."},
	"distance_since_clear_km":     {"Distance reported since diagnostic trouble codes were cleared.", "This is a resettable diagnostic counter, not the vehicle's lifetime odometer."},
	"distance_with_mil_km":        {"Distance accumulated while the malfunction indicator was active.", "This describes driving with the reported warning state. It does not identify the fault or replace the lifetime odometer."},
}

func explainSignal(metric string) (string, string) {
	if e, ok := signalExplanations[metric]; ok {
		return e.description, e.interpretation
	}
	switch {
	case strings.HasPrefix(metric, "readiness_") && strings.HasSuffix(metric, "_ready"):
		name := strings.ReplaceAll(strings.TrimSuffix(strings.TrimPrefix(metric, "readiness_"), "_ready"), "_", " ")
		return "Completion state of the vehicle's " + name + " emissions readiness check.", "Ready means the check has completed; not ready means it has not completed. Neither state alone is a pass/fail diagnosis. Unsupported checks remain absent, not false."
	case strings.HasPrefix(metric, "tire_") && strings.HasSuffix(metric, "_kpa"):
		return "Air pressure reported for the tire identified in this reading's name.", "Compare cold-tire pressure with the vehicle's placard. Pressure changes with temperature and driving. The timestamp matters because the provider may report an older reading."
	case strings.Contains(metric, "fuel_trim"):
		term := "short-term correction"
		if strings.HasPrefix(metric, "long_") {
			term = "learned long-term correction"
		}
		return "The engine controller's " + term + " to its base fuel delivery.", "Positive means fuel is being added; negative means fuel is being removed. Compare the same bank under similar load and temperature. Fuel trim is not the tank's fuel percentage."
	case strings.HasPrefix(metric, "o2_sensor_") && strings.HasSuffix(metric, "_trim_pct"):
		return "Fuel correction reported alongside this oxygen-sensor channel.", "Positive adds fuel and negative removes fuel relative to the base calculation. Sensor numbering follows the source's OBD channels, not a guessed physical location."
	case strings.HasPrefix(metric, "o2_sensor_") && strings.HasSuffix(metric, "_v"):
		return "Electrical voltage reported by this oxygen-sensor channel.", "Use the engine's sensor layout and diagnostic procedure to interpret it. Channel numbers do not establish bank or upstream/downstream position, and voltage is not an air-to-fuel ratio."
	case strings.HasPrefix(metric, "catalyst_") && strings.HasSuffix(metric, "_c"):
		return "Catalytic-converter temperature for the bank and sensor identified by this channel.", "Exhaust conditions and sensor position affect temperature. Compare the same channel under similar load; a temperature reading alone does not measure catalyst efficiency."
	case metric == "fuel_system_1_status" || metric == "fuel_system_2_status":
		return "The fuel-control operating mode reported for this fuel system.", "Closed loop uses oxygen-sensor feedback to adjust fueling. Open loop can occur during warm-up or particular driving conditions; some distinct modes indicate a detected fault. Read the named state rather than treating its numeric code as a quantity."
	case metric == "throttle_b_pct" || metric == "throttle_c_pct":
		return "Throttle position reported by the named secondary sensor channel.", "This channel may use a different calibration from the primary throttle sensor. It is not automatically a second throttle or an accelerator-pedal reading."
	case strings.HasPrefix(metric, "pedal_"):
		return "Accelerator-pedal position from the named sensor channel.", "Multiple pedal channels can have different scales for plausibility checking. Compare the same channel over time rather than expecting every channel to match."
	}
	return "", ""
}
