import SwiftUI
import UIKit
import ImageIO
import UniformTypeIdentifiers
import CryptoKit

enum VehiclePhotoData {
    static let maximumBytes = 2 * 1024 * 1024

    static func normalizedJPEG(_ data: Data) throws -> Data {
        guard data.count <= 32 * 1024 * 1024,
              let source = CGImageSourceCreateWithData(data as CFData, nil),
              let image = CGImageSourceCreateThumbnailAtIndex(source, 0, [
                kCGImageSourceCreateThumbnailFromImageAlways: true,
                kCGImageSourceCreateThumbnailWithTransform: true,
                kCGImageSourceThumbnailMaxPixelSize: 1600,
                kCGImageSourceShouldCacheImmediately: true
              ] as CFDictionary) else {
            throw APIError.message("Choose a photo smaller than 32 MB that your phone can open.")
        }
        for quality in [0.85, 0.65, 0.45] {
            let output = NSMutableData()
            guard let destination = CGImageDestinationCreateWithData(output as CFMutableData, UTType.jpeg.identifier as CFString, 1, nil) else { break }
            // Re-encode pixels without copying GPS, camera, or other source metadata.
            CGImageDestinationAddImage(destination, image, [kCGImageDestinationLossyCompressionQuality: quality] as CFDictionary)
            if CGImageDestinationFinalize(destination), output.length <= maximumBytes { return output as Data }
        }
        throw APIError.message("This photo is too large. Choose a smaller image and try again.")
    }
}

actor VehiclePhotoCache {
    static let shared = VehiclePhotoCache()
    private var generation = 0
    private var directory: URL {
        let name = ProcessInfo.processInfo.arguments.contains("--ui-testing") ? "vehicle-photos-ui-test" : "vehicle-photos"
        return FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0].appendingPathComponent(name, isDirectory: true)
    }

    func image(vehicle: Vehicle, client: APIClient) async throws -> Data {
        guard let revision = vehicle.photoRevision else { throw APIError.message("No vehicle photo.") }
        let key = prefix(vehicleID: vehicle.id, connection: client.connection) + digest(revision)
        let file = directory.appendingPathComponent(key + ".jpg")
        if let saved = try? Data(contentsOf: file), saved.count <= VehiclePhotoData.maximumBytes, UIImage(data: saved) != nil { return saved }
        let requestedGeneration = generation
        let image = try await client.send("vehicles/\(vehicle.id)/photo", accept: "image/jpeg")
        try Task.checkCancellation()
        guard requestedGeneration == generation else { throw CancellationError() }
        guard image.count <= VehiclePhotoData.maximumBytes, UIImage(data: image) != nil else { throw APIError.message("The vehicle photo could not be opened.") }
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        var excluded = directory
        var values = URLResourceValues(); values.isExcludedFromBackup = true
        try excluded.setResourceValues(values)
        try image.write(to: file, options: [.atomic, .completeFileProtection])
        return image
    }

    private func digest(_ text: String) -> String { SHA256.hash(data: Data(text.utf8)).map { String(format: "%02x", $0) }.joined() }
    private func prefix(vehicleID: String, connection: Connection) -> String { digest(connection.cacheIdentity + "/" + vehicleID) + "-" }

    func remove(vehicleID: String, connection: Connection) throws {
        generation += 1
        guard FileManager.default.fileExists(atPath: directory.path) else { return }
        let ownedPrefix = prefix(vehicleID: vehicleID, connection: connection)
        for file in try FileManager.default.contentsOfDirectory(at: directory, includingPropertiesForKeys: nil) where file.lastPathComponent.hasPrefix(ownedPrefix) {
            try FileManager.default.removeItem(at: file)
        }
    }

    func clear() throws {
        generation += 1
        if FileManager.default.fileExists(atPath: directory.path) { try FileManager.default.removeItem(at: directory) }
    }
}

struct VehiclePhotoView: View {
    @EnvironmentObject private var store: GarageStore
    let vehicle: Vehicle
    @State private var image: UIImage?
    @State private var failed = false
    @State private var retry = 0

    var body: some View {
        Group {
            if let image { Image(uiImage: image).resizable().scaledToFill() }
            else {
                ZStack {
                    LinearGradient(colors: [PitStyle.panel, PitStyle.background], startPoint: .topLeading, endPoint: .bottomTrailing)
                    VStack(spacing: 10) {
                        Image(systemName: "car.side.fill").font(.system(size: 46, weight: .light)).foregroundStyle(PitStyle.amber)
                        if failed { Text("Photo unavailable").font(.caption).foregroundStyle(.secondary) }
                    }
                }
            }
        }
        .frame(maxWidth: .infinity).frame(height: 170).clipped()
        .accessibilityLabel("Photo of \(vehicle.name)")
        .accessibilityIdentifier("vehiclePhoto")
        .task(id: "\(store.connection?.cacheIdentity ?? "")/\(vehicle.photoRevision ?? "")/\(retry)") {
            image = nil; failed = false
            guard vehicle.photoRevision != nil, let connection = store.connection else { return }
            do {
                let data = try await VehiclePhotoCache.shared.image(vehicle: vehicle, client: APIClient(connection: connection))
                try Task.checkCancellation()
                image = UIImage(data: data)
            } catch is CancellationError { }
            catch { failed = true }
        }
        .contextMenu { if failed { Button("Retry photo", systemImage: "arrow.clockwise") { retry += 1 } } }
    }
}
