//go:build desktop

package bootstrap

import (
	"context"
	"io/fs"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/JeropsWang/local-project-hub/internal/desktop/services"
	desktopwindow "github.com/JeropsWang/local-project-hub/internal/desktop/window"
)

// Run binds the native shell. Closing its window only changes visibility.
func Run(assets fs.FS) error {
	manager := services.NewManager(newSession)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	app := application.New(application.Options{
		Name:       "Local Project Hub",
		Assets:     application.AssetOptions{Handler: application.BundledAssetFileServer(assets)},
		Services:   []application.Service{application.NewService(manager)},
		ShouldQuit: func() bool { allowed, err := manager.CanQuit(ctx); return err == nil && allowed },
	})
	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title: "Local Project Hub", Width: 1160, Height: 780, MinWidth: 960, MinHeight: 640, URL: "/",
	})
	controller := desktopwindow.Controller{Hide: func() { window.Hide() }, Quit: app.Quit}
	window.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
		controller.Close()
		event.Cancel()
	})
	tray := app.SystemTray.New()
	tray.SetTooltip("Local Project Hub")
	tray.OnClick(func() { window.Show() })
	menu := app.NewMenu()
	menu.Add("显示工作台").OnClick(func(*application.Context) { window.Show() })
	menu.Add("安全退出").OnClick(func(*application.Context) {
		if manager.Workspace() == nil {
			app.Quit()
			return
		}
		window.Show()
		app.Event.Emit("manager:request-exit")
	})
	tray.SetMenu(menu)
	go func() {
		ticker := time.NewTicker(300 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if manager.Workspace() == nil {
					continue
				}
				allowed, err := manager.CanQuit(ctx)
				if err == nil && controller.FinishQuit(allowed) {
					return
				}
			}
		}
	}()
	return app.Run()
}
