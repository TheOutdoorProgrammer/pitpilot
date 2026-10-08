import AppKit
import ImageIO
import UniformTypeIdentifiers

// Render the instrument-dial mark at source resolution for reproducible app assets.
let size = 1024
let context = CGContext(data: nil, width: size, height: size, bitsPerComponent: 8,
    bytesPerRow: size * 4, space: CGColorSpaceCreateDeviceRGB(),
    bitmapInfo: CGImageAlphaInfo.noneSkipLast.rawValue)!
NSGraphicsContext.saveGraphicsState()
NSGraphicsContext.current = NSGraphicsContext(cgContext: context, flipped: false)
context.setFillColor(NSColor(red: 0.055, green: 0.065, blue: 0.075, alpha: 1).cgColor)
context.fill(CGRect(x: 0, y: 0, width: size, height: size))
let amber = NSColor(red: 1, green: 0.73, blue: 0.24, alpha: 1)
context.setStrokeColor(amber.cgColor)
context.setLineWidth(42)
context.setLineCap(.round)
context.addArc(center: CGPoint(x: 512, y: 500), radius: 320, startAngle: .pi * 0.85, endAngle: .pi * 0.15, clockwise: true)
context.strokePath()
for index in 0...8 {
    let angle = Double.pi * (0.15 + Double(index) * 0.7 / 8)
    let x = cos(angle), y = sin(angle)
    context.setLineWidth(index % 2 == 0 ? 16 : 9)
    context.move(to: CGPoint(x: 512 + x * 252, y: 500 + y * 252))
    context.addLine(to: CGPoint(x: 512 + x * 282, y: 500 + y * 282))
    context.strokePath()
}
context.setStrokeColor(NSColor.white.cgColor)
context.setLineWidth(34)
context.move(to: CGPoint(x: 420, y: 370))
context.addLine(to: CGPoint(x: 665, y: 650))
context.strokePath()
context.setFillColor(amber.cgColor)
context.fillEllipse(in: CGRect(x: 466, y: 454, width: 92, height: 92))
let paragraph = NSMutableParagraphStyle()
paragraph.alignment = .center
let word = "PITPILOT" as NSString
word.draw(in: CGRect(x: 100, y: 190, width: 824, height: 110), withAttributes: [.font: NSFont.systemFont(ofSize: 72, weight: .black), .foregroundColor: NSColor.white, .kern: 12, .paragraphStyle: paragraph])
NSGraphicsContext.restoreGraphicsState()
let destination = CGImageDestinationCreateWithURL(URL(fileURLWithPath: CommandLine.arguments[1]) as CFURL, UTType.png.identifier as CFString, 1, nil)!
CGImageDestinationAddImage(destination, context.makeImage()!, nil)
guard CGImageDestinationFinalize(destination) else { fatalError("Unable to write app icon") }
