import AppKit

// Build-time rendering. The font remains on the build machine.
guard CommandLine.arguments.count == 3, let size = Int(CommandLine.arguments[2]), size > 0 else {
    fatalError("Usage: render-app-emoji.swift output.png size")
}
let bitmap = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: size, pixelsHigh: size,
    bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true, isPlanar: false,
    colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0)!
bitmap.size = NSSize(width: size, height: size)
guard let font = NSFont(name: "Apple Color Emoji", size: CGFloat(size) * 0.78) else {
    fatalError("Apple Color Emoji is required to generate the application icon.")
}
NSGraphicsContext.saveGraphicsState()
NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: bitmap)
let text = NSAttributedString(string: "💬", attributes: [.font: font])
let extent = text.size()
text.draw(at: NSPoint(x: (CGFloat(size) - extent.width) / 2, y: (CGFloat(size) - extent.height) / 2))
NSGraphicsContext.restoreGraphicsState()
try bitmap.representation(using: .png, properties: [:])!.write(to: URL(fileURLWithPath: CommandLine.arguments[1]))
