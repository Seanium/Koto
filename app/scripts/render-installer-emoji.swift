import AppKit

// Build-time rendering only. The system font stays on the build machine.
let scale = Int(CommandLine.arguments[3])!
let size = NSSize(width: 660, height: 410)
let bitmap = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: 660 * scale,
    pixelsHigh: 410 * scale, bitsPerSample: 8, samplesPerPixel: 4,
    hasAlpha: true, isPlanar: false, colorSpaceName: .deviceRGB,
    bytesPerRow: 0, bitsPerPixel: 0)!
bitmap.size = size
guard let font = NSFont(name: "Apple Color Emoji", size: 48) else {
    fatalError("Apple Color Emoji is required to generate the installer artwork.")
}
NSGraphicsContext.saveGraphicsState()
NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: bitmap)
NSImage(contentsOfFile: CommandLine.arguments[1])!.draw(in: NSRect(origin: .zero, size: size))
for (emoji, centerX, centerY) in [("🐙", CGFloat(186), CGFloat(147)), ("🐳", CGFloat(474), CGFloat(147)), ("➡️", CGFloat(330), CGFloat(226))] {
    let text = NSAttributedString(string: emoji, attributes: [.font: font])
    let textSize = text.size()
    text.draw(at: NSPoint(x: centerX - textSize.width / 2, y: size.height - centerY - textSize.height / 2))
}
NSGraphicsContext.restoreGraphicsState()
try bitmap.representation(using: .png, properties: [:])!.write(to: URL(fileURLWithPath: CommandLine.arguments[2]))
