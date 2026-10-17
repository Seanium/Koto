//go:build darwin

package main

// macOS title bar, native emoji icons, appearance, menus, and application discovery.

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Cocoa
#import <Cocoa/Cocoa.h>
#include <stdlib.h>
#include <string.h>

// Render the character with the font installed on this Mac. The encoded image only
// lives in memory; no Apple font, glyph asset, bundle resource, or Finder icon is written.
static void *kotoEmojiPNG(int pixels, int *length) {
	*length = 0;
	@autoreleasepool {
		// Small Apple Color Emoji strikes can extend beyond their layout bounds.
		// Downsample the same padded, high-resolution rendering used for the Dock.
		int renderPixels = MAX(pixels, 512);
		NSFont *font = [NSFont fontWithName:@"Apple Color Emoji" size:renderPixels * 0.82];
		if (font == nil) return NULL;
		NSBitmapImageRep *bitmap = [[NSBitmapImageRep alloc]
			initWithBitmapDataPlanes:NULL pixelsWide:renderPixels pixelsHigh:renderPixels
			bitsPerSample:8 samplesPerPixel:4 hasAlpha:YES isPlanar:NO
			colorSpaceName:NSCalibratedRGBColorSpace bytesPerRow:0 bitsPerPixel:0];
		if (bitmap == nil) return NULL;
		NSGraphicsContext *context = [NSGraphicsContext graphicsContextWithBitmapImageRep:bitmap];
		[NSGraphicsContext saveGraphicsState];
		[NSGraphicsContext setCurrentContext:context];
		CGContextClearRect(context.CGContext, CGRectMake(0, 0, renderPixels, renderPixels));
		NSString *emoji = @"\U0001F4AC";
		NSDictionary *attributes = @{NSFontAttributeName: font};
		NSSize size = [emoji sizeWithAttributes:attributes];
		[emoji drawAtPoint:NSMakePoint((renderPixels - size.width) / 2.0,
			(renderPixels - size.height) / 2.0) withAttributes:attributes];
		[NSGraphicsContext restoreGraphicsState];
		if (renderPixels != pixels) {
			NSBitmapImageRep *scaled = [[NSBitmapImageRep alloc]
				initWithBitmapDataPlanes:NULL pixelsWide:pixels pixelsHigh:pixels
				bitsPerSample:8 samplesPerPixel:4 hasAlpha:YES isPlanar:NO
				colorSpaceName:NSCalibratedRGBColorSpace bytesPerRow:0 bitsPerPixel:0];
			if (scaled == nil) return NULL;
			NSGraphicsContext *smallContext = [NSGraphicsContext graphicsContextWithBitmapImageRep:scaled];
			[NSGraphicsContext saveGraphicsState];
			[NSGraphicsContext setCurrentContext:smallContext];
			smallContext.imageInterpolation = NSImageInterpolationHigh;
			CGContextClearRect(smallContext.CGContext, CGRectMake(0, 0, pixels, pixels));
			CGFloat margin = MAX(2.0, pixels * 0.04);
			[bitmap drawInRect:NSInsetRect(NSMakeRect(0, 0, pixels, pixels), margin, margin)];
			[NSGraphicsContext restoreGraphicsState];
			bitmap = scaled;
		}
		NSData *png = [bitmap representationUsingType:NSBitmapImageFileTypePNG properties:@{}];
		if (png.length == 0 || png.length > INT_MAX) return NULL;
		void *result = malloc(png.length);
		if (result == NULL) return NULL;
		memcpy(result, png.bytes, png.length);
		*length = (int)png.length;
		return result;
	}
}

// 1 when LaunchServices knows an app with this bundle id.
static int kotoAppInstalled(const char *bid) {
	NSURL *u = [[NSWorkspace sharedWorkspace] URLForApplicationWithBundleIdentifier:[NSString stringWithUTF8String:bid]];
	return u != nil;
}

static void kotoSetAppearance(int mode) {
	dispatch_async(dispatch_get_main_queue(), ^{
		NSApp.appearance = mode == 0 ? nil : [NSAppearance appearanceNamed:(mode == 2 ? NSAppearanceNameDarkAqua : NSAppearanceNameAqua)];
	});
}
*/
import "C"

import (
	"log"
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/application"
)

func mainWindowOptions() application.WebviewWindowOptions {
	return application.WebviewWindowOptions{
		Name: "main", Title: windowTitle(), Width: 1280, Height: 800, MinWidth: 1120, MinHeight: 680, URL: "/",
		DefaultContextMenuDisabled: true,
		BackgroundType:             application.BackgroundTypeSolid, BackgroundColour: application.NewRGB(255, 255, 255),
		Mac: application.MacWindow{
			TitleBar:                application.MacTitleBarHiddenInset,
			InvisibleTitleBarHeight: 44,
		},
	}
}

func nativeEmojiPNG(pixels int) []byte {
	var length C.int
	data := C.kotoEmojiPNG(C.int(pixels), &length)
	if data == nil {
		return nil
	}
	defer C.free(data)
	return C.GoBytes(data, length)
}

func platformApplicationIcon() []byte {
	icon := nativeEmojiPNG(512)
	if len(icon) == 0 {
		log.Print("Apple Color Emoji is unavailable; retaining the bundle application icon")
	}
	return icon
}

func setTrayIcon(t *application.SystemTray, _ []byte) {
	if icon := nativeEmojiPNG(64); len(icon) != 0 {
		// Keep the system emoji's colors and shading; a template image would flatten it.
		t.SetIcon(icon)
		return
	}
	t.SetLabel("💬")
}

// setAppearance: "light", "dark", or "" (follow the system).
func setAppearance(mode string) {
	m := 0
	switch mode {
	case "light":
		m = 1
	case "dark":
		m = 2
	}
	C.kotoSetAppearance(C.int(m))
}

// appMenu: the standard macOS app menu with 设置… (⌘,), plus Edit (copy/paste in inputs) and Window.
func appMenu(ui *UI) *application.Menu {
	m := application.NewMenu()
	app := m.AddSubmenu(productName)
	// Wails' roles label these in English; same actions, our words.
	ui.localizedItem(app.Add(ui.text("关于 ")+productName), "关于 ", productName).OnClick(func(*application.Context) { ui.app.Menu.ShowAbout() })
	app.AddSeparator()
	ui.localizedItem(app.Add(ui.text("设置…")), "设置…", "").SetAccelerator("CmdOrCtrl+,").OnClick(func(*application.Context) { ui.openSettings("") })
	app.AddSeparator()
	ui.localizedItem(app.Add(ui.text("隐藏 ")+productName), "隐藏 ", productName).SetAccelerator("CmdOrCtrl+h").SetRole(application.Hide)
	ui.localizedItem(app.Add(ui.text("隐藏其他")), "隐藏其他", "").SetAccelerator("CmdOrCtrl+OptionOrAlt+h").SetRole(application.HideOthers)
	app.AddSeparator()
	ui.localizedItem(app.Add(ui.text("退出 ")+productName), "退出 ", productName).SetAccelerator("CmdOrCtrl+q").OnClick(func(*application.Context) { ui.app.Quit() })
	m.AddRole(application.EditMenu)
	m.AddRole(application.WindowMenu)
	return m
}

// ---------------------------------------------------------------- openers (see openers.go)

const fileManagerName = "Finder"

func appInstalled(bundleID string) bool {
	c := C.CString(bundleID)
	defer C.free(unsafe.Pointer(c))
	return C.kotoAppInstalled(c) == 1
}

// Terminal.app and iTerm2 run the line in a new window's shell; Ghostty (1.3+) types it into a new
// window's shell; Warp cannot be told what to run, so it opens at the folder with the line copied.
const terminalAppScript = `tell application "Terminal"
	activate
	do script "{cmd_as}"
end tell`

func terminalPresets() []Opener {
	return []Opener{
		{ID: "system", Name: "系统默认", argv: []string{"osascript", "-e", terminalAppScript}},
		{ID: "terminal", Name: "Terminal", detect: "com.apple.Terminal", argv: []string{"osascript", "-e", terminalAppScript}},
		{ID: "iterm2", Name: "iTerm2", detect: "com.googlecode.iterm2", argv: []string{"osascript", "-e", `tell application "iTerm"
	activate
	set w to (create window with default profile)
	tell current session of w to write text "{cmd_as}"
end tell`}},
		{ID: "ghostty", Name: "Ghostty", detect: "com.mitchellh.ghostty", argv: []string{"osascript", "-e", `tell application "Ghostty"
	activate
	set cfg to new surface configuration
	set initial working directory of cfg to "{cwd_as}"
	set initial input of cfg to "{cmd_as}" & linefeed
	new window with configuration cfg
end tell`}},
		{ID: "warp", Name: "Warp", detect: "dev.warp.Warp-Stable", argv: []string{"open", "warp://action/new_window?path={cwd_url}"}, clip: true},
	}
}

func editorPresets() []Opener {
	ed := func(id, name, bundle string) Opener {
		return Opener{ID: id, Name: name, detect: bundle, argv: []string{"open", "-b", bundle, "{path}"}}
	}
	return []Opener{
		ed("vscode", "VS Code", "com.microsoft.VSCode"),
		ed("cursor", "Cursor", "com.todesktop.230313mzl4w4u92"),
		ed("zed", "Zed", "dev.zed.Zed"),
		ed("xcode", "Xcode", "com.apple.dt.Xcode"),
	}
}

func revealPath(p string) bool { return run([]string{"open", "-R", p}) }
