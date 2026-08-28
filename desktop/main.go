package main

import (
	"embed"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed frontend/dist/index.html frontend/dist/assets/*
var assets embed.FS

const (
	ephyRuntimeTitle            = "Ephy Runtime"
	ephyRuntimeSingleInstanceID = "com.wails.ephy-runtime"
)

func newApplicationOptions(app *App) *options.App {
	return &options.App{
		Title:     ephyRuntimeTitle,
		Width:     1320,
		Height:    920,
		MinWidth:  1024,
		MinHeight: 760,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 244, G: 238, B: 225, A: 1},
		OnStartup:        app.startup,
		OnShutdown:       app.shutdown,
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId: ephyRuntimeSingleInstanceID,
			OnSecondInstanceLaunch: func(_ options.SecondInstanceData) {
				app.showExistingWindow()
			},
		},
		Bind: []interface{}{
			app,
		},
	}
}

func main() {
	app := NewApp()
	err := wails.Run(newApplicationOptions(app))

	if err != nil {
		println("Error:", err.Error())
	}
}
