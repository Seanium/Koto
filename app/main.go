package main

// koto desktop app: a Wails v3 shell (system webview) with the core in this process. Main window +
// menu-bar/tray icon (opens the main window); platform specifics in platform_*.go.

import (
	"embed"
	"log"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

//go:embed all:frontend/dist
var assets embed.FS

type UI struct {
	app        *application.App
	main       *application.WebviewWindow
	tray       *application.SystemTray
	prefs      *prefStore
	locale     atomic.Value
	localeMu   sync.Mutex
	menuLabels []menuLabel
}

// openSettings shows the main window on its settings view (tab "" keeps the last one).
func (u *UI) openSettings(tab string) {
	u.openMain("")
	u.app.Event.Emit("settings:tab", tab)
}

// keepOnClose makes closing a window hide it (the app lives on in the menu bar / tray).
func keepOnClose(w *application.WebviewWindow) {
	w.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		w.Hide()
		e.Cancel()
	})
}

func (u *UI) openMain(nav string) {
	u.main.Show()
	u.main.Focus()
	if nav != "" {
		u.app.Event.Emit("app:navigate", nav)
	}
}

func withFrame(o application.WebviewWindowOptions, f *WindowFrame) application.WebviewWindowOptions {
	if f != nil && f.W >= o.MinWidth && f.H >= o.MinHeight {
		o.Width, o.Height, o.X, o.Y = f.W, f.H, f.X, f.Y
		o.InitialPosition = application.WindowXY
	}
	return o
}

// sessionMenu is the native right-click menu on a session row (CSS --custom-contextmenu: session,
// --custom-contextmenu-data: <thread id>). Items are handled by the frontend via "app:context".
func sessionMenu(ui *UI) {
	app := ui.app
	m := application.NewContextMenu("session")
	item := func(label, action string) {
		ui.localizedItem(m.Add(label).OnClick(func(ctx *application.Context) {
			app.Event.Emit("app:context", navJSON(map[string]string{"action": action, "thread": strings.TrimSpace(ctx.ContextMenuData())}))
		}), label, "")
	}
	item("复制会话 ID", "copy-id")
	m.AddSeparator()
	item("移到回收站", "trash")
}

func main() {
	if previewSandbox != "" {
		// A preview always runs on the project sandbox, whatever the environment says.
		for k, v := range map[string]string{"KOTO_APP_SANDBOX": previewSandbox, "PATH": previewPath} {
			if v != "" {
				_ = os.Setenv(k, v)
			}
		}
	}
	if previewSandbox == "" {
		adoptLoginPath()
	}
	platformSmokeResult := os.Getenv("KOTO_APP_PLATFORM_SMOKE")
	if platformSmokeResult != "" {
		if err := validatePlatformSmoke(); err != nil {
			log.Fatal(err)
		}
	}
	// An app opened from Finder has no locale: osascript then reads its script as MacRoman and a
	// UTF-8 path (中文, emoji) reaches the terminal garbled. Give child processes a UTF-8 one.
	if os.Getenv("LANG") == "" && os.Getenv("LC_ALL") == "" {
		_ = os.Setenv("LANG", "en_US.UTF-8")
	}
	if os.Getenv("LC_CTYPE") == "" {
		_ = os.Setenv("LC_CTYPE", "UTF-8")
	}
	ui := &UI{}
	marks := make(chan string, 32)
	prefs := loadPrefs()
	core := &Core{LiveMs: func() *int64 { return prefs.Get().liveMs() }}
	svc := &AppService{core: core, ui: ui, prefs: prefs, mark: func(m string) {
		select {
		case marks <- m:
		default:
		}
	}}

	app := application.New(application.Options{
		Name:        productName,
		Description: productDescription,
		Icon:        platformApplicationIcon(),
		Services:    []application.Service{application.NewService(svc)},
		Assets:      application.AssetOptions{Handler: application.AssetFileServerFS(assets)},
		Mac:         application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: false},
	})
	ui.app = app
	ui.prefs = prefs
	core.OnEvent = func(raw string) { app.Event.Emit("koto:event", raw) }
	core.OnStatus = func(s CoreStatus) { app.Event.Emit("koto:status", s) }

	setAppearance(prefs.Get().Appearance)
	ui.main = app.Window.NewWithOptions(withFrame(mainWindowOptions(), prefs.Get().Window))
	// Native fullscreen/restore and programmatic resize can clear Wails' constraints. Reapply
	// the product minimum after those transitions, and repair an undersized frame immediately.
	minimum := mainWindowOptions()
	enforceMinimum := func() {
		if !ui.main.IsFullscreen() {
			w, h := ui.main.Size()
			if w < minimum.MinWidth || h < minimum.MinHeight {
				ui.main.SetSize(max(w, minimum.MinWidth), max(h, minimum.MinHeight))
			}
			ui.main.SetMinSize(minimum.MinWidth, minimum.MinHeight)
		}
	}
	for _, ev := range []events.WindowEventType{events.Common.WindowShow, events.Common.WindowRestore, events.Common.WindowUnFullscreen} {
		ui.main.OnWindowEvent(ev, func(*application.WindowEvent) { enforceMinimum() })
	}
	ui.main.OnWindowEvent(events.Common.WindowDidResize, func(*application.WindowEvent) {
		w, h := ui.main.Size()
		if w < minimum.MinWidth || h < minimum.MinHeight {
			enforceMinimum()
		}
	})
	keepOnClose(ui.main)
	saveFrame := func() {
		x, y := ui.main.Position()
		w, h := ui.main.Size()
		p := prefs.Get()
		p.Window = &WindowFrame{x, y, w, h}
		_ = prefs.Set(p)
	}
	ui.main.RegisterHook(events.Common.WindowClosing, func(*application.WindowEvent) { saveFrame() })
	sessionMenu(ui)
	if m := appMenu(ui); m != nil {
		app.Menu.Set(m)
	}

	// The menu-bar icon opens the main window (no mini window for now).
	ui.tray = app.SystemTray.New()
	setTrayIcon(ui.tray, trayIconPNG)
	ui.tray.SetTooltip(productName)
	ui.tray.OnClick(func() { ui.openMain("") })

	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		enforceMinimum()
		go func() {
			core.Start()
			if platformSmokeResult != "" {
				platformSmoke(platformSmokeResult, ui, core, marks)
			}
		}()
	})

	// Quit can exit the process before Run returns: let go of the data here.
	app.OnShutdown(func() { saveFrame(); core.Stop() })
	err := app.Run()
	core.Stop()
	if err != nil {
		log.Fatal(err)
	}
}
