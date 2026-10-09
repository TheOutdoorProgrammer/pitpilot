import Foundation

struct VehicleProfileDraft: Equatable {
    var name: String
    var make: String
    var model: String
    var year: Int
    var odometerMiles: Double
    var odometerStatus: String
    var vin: String
    var licensePlate: String

    func changes(since acknowledged: Self?) -> [String: Any] {
        var values: [String: Any] = [:]
        let cleanName = name.trimmingCharacters(in: .whitespacesAndNewlines)
        let cleanVIN = vin.trimmingCharacters(in: .whitespacesAndNewlines)
        let cleanPlate = licensePlate.trimmingCharacters(in: .whitespacesAndNewlines)
        if acknowledged?.name.trimmingCharacters(in: .whitespacesAndNewlines) != cleanName { values["name"] = cleanName }
        if acknowledged?.make != make { values["make"] = make }
        if acknowledged?.model != model { values["model"] = model }
        if acknowledged?.year != year { values["year"] = year }
        if acknowledged?.odometerMiles != odometerMiles { values["odometerMiles"] = odometerMiles }
        if acknowledged?.odometerStatus != odometerStatus { values["odometerStatus"] = odometerStatus }
        if acknowledged?.vin.trimmingCharacters(in: .whitespacesAndNewlines) != cleanVIN { values["vin"] = cleanVIN }
        if acknowledged?.licensePlate.trimmingCharacters(in: .whitespacesAndNewlines) != cleanPlate { values["licensePlate"] = cleanPlate }
        return values
    }
}

extension VehicleProfileDraft {
    init(vehicle: Vehicle) {
        self.init(name: vehicle.name, make: vehicle.make, model: vehicle.model, year: vehicle.year,
                  odometerMiles: vehicle.odometerMiles, odometerStatus: vehicle.odometerStatus ?? "unknown",
                  vin: vehicle.vin ?? "", licensePlate: vehicle.licensePlate ?? "")
    }
}
