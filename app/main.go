package main

// koto desktop app: a Wails v3 shell (system webview) with the core in this process. Main window +
// menu-bar/tray icon (opens the main window); platform specifics in platform_*.go.

import (
	"embed"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"koto/internal/update"

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
	opening    windowOpenQueue
}

// openSettings shows the main window on its settings view (tab "" opens General).
func (u *UI) openSettings(tab string) {
	u.requestOpen(windowOpenRequest{settings: &tab})
}

// keepOnClose makes closing a window hide it (the app lives on in the menu bar / tray).
func keepOnClose(w *application.WebviewWindow) {
	w.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		w.Hide()
		e.Cancel()
	})
}

func (u *UI) openMain(nav string) {
	u.requestOpen(windowOpenRequest{nav: nav})
}

func (u *UI) requestOpen(request windowOpenRequest) {
	// Wait for the actual React listeners, which also guarantees WebView2 is
	// initialized. A single queue keeps a cold-start tray request and its target.
	if u.opening.request(request) {
		u.showRequest(request)
	}
}

func (u *UI) frontendReady() {
	if pending := u.opening.activate(); pending != nil {
		u.showRequest(*pending)
	}
}

func (u *UI) showRequest(request windowOpenRequest) {
	u.main.Show()
	u.main.Focus()
	if request.settings != nil {
		u.app.Event.Emit("settings:tab", *request.settings)
	} else if request.nav != "" {
		u.app.Event.Emit("app:navigate", request.nav)
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
// --custom-contextmenu-data: <JSON string>). Quoting prevents CSS token serialization
// from inserting comments into IDs that contain numbers. The frontend handles actions.
func sessionMenu(ui *UI) {
	app := ui.app
	m := application.NewContextMenu("session")
	item := func(label, action string) {
		ui.localizedItem(m.Add(label).OnClick(func(ctx *application.Context) {
			thread, err := contextSessionID(ctx.ContextMenuData())
			if err != nil {
				return
			}
			app.Event.Emit("app:context", navJSON(map[string]string{"action": action, "thread": thread}))
		}), label, "")
	}
	item("复制会话 ID", "copy-id")
}

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--koto-update-version" {
		fmt.Println("koto", strings.TrimSpace(appVersionFile))
		return
	}
	if len(os.Args) == 3 && os.Args[1] == "--koto-update-recover" {
		if err := update.RunRecovery(os.Args[2]); err != nil {
			log.Print(err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) == 3 && os.Args[1] == "--koto-update-restart" {
		if err := update.RunRestart(os.Args[2]); err != nil {
			log.Print(err)
			os.Exit(1)
		}
		return
	}
	update.CleanPrevious(update.App)
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
	core := &Core{}
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
	if !platformRuntimeReady(app, prefs.Get().Language) {
		return
	}
	ui.app = app
	svc.updates = newAppUpdater(func(state UpdateState) { app.Event.Emit("koto:update", state) }, app.Quit)
	ui.prefs = prefs
	core.OnEvent = func(raw string) { app.Event.Emit("koto:event", raw) }
	core.OnStatus = func(s CoreStatus) { app.Event.Emit("koto:status", s) }

	setAppearance(ui, prefs.Get().Appearance)
	ui.main = app.Window.NewWithOptions(withFrame(mainWindowOptions(), prefs.Get().Window))
	bindWindowAppearance(ui)
	// Native fullscreen/restore and programmatic resize can clear Wails' constraints. Reapply
	// the product minimum after those transitions, and repair an undersized frame immediately.
	minimum := mainWindowOptions()
	var lastWorkArea atomic.Value
	enforceMinimum := func() {
		if ui.main.IsMinimised() || ui.main.IsFullscreen() {
			return
		}
		enforceWindowFrame(ui.main, minimum.MinWidth, minimum.MinHeight)
		if area, ok := windowWorkArea(ui.main); ok {
			lastWorkArea.Store(area)
		}
	}
	for _, ev := range []events.WindowEventType{events.Common.WindowShow, events.Common.WindowRestore, events.Common.WindowUnFullscreen, events.Common.WindowUnMaximise, events.Common.WindowUnMinimise, events.Common.WindowDPIChanged} {
		ui.main.OnWindowEvent(ev, func(*application.WindowEvent) { enforceMinimum() })
	}
	// Clamp on a monitor/work-area change, not every move: ordinary dragging
	// must still let the window cross a display boundary.
	ui.main.OnWindowEvent(events.Common.WindowDidMove, func(*application.WindowEvent) {
		if ui.main.IsMinimised() || ui.main.IsFullscreen() {
			return
		}
		if area, ok := windowWorkArea(ui.main); ok {
			before := lastWorkArea.Swap(area)
			if before != nil && before.(application.Rect) != area {
				enforceMinimum()
			}
		}
	})
	ui.main.OnWindowEvent(events.Common.WindowDidResize, func(*application.WindowEvent) {
		w, h := ui.main.Size()
		minWidth, minHeight := windowMinimum(ui.main, minimum.MinWidth, minimum.MinHeight)
		if w < minWidth || h < minHeight {
			enforceMinimum()
		}
	})
	ui.main.OnWindowEvent(events.Common.WindowRuntimeReady, func(*application.WindowEvent) {
		enforceMinimum()
	})
	keepOnClose(ui.main)
	saveFrame := func() {
		if ui.main.IsMinimised() || ui.main.IsMaximised() || ui.main.IsFullscreen() {
			return
		}
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
	app.OnShutdown(func() { svc.updates.cancel(); saveFrame(); core.Stop() })
	if platformSmokeResult != "" {
		// Exercise a real cold-start menu request before the WebView exists.
		ui.openSettings("general")
	}
	err := app.Run()
	svc.updates.cancel()
	core.Stop()
	if err != nil {
		log.Fatal(err)
	}
}
