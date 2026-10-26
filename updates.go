package main

import (
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/updater"
)

// AppInfo tells the page about the app and checks for new versions.
type AppInfo struct{}

// About is the version of the app and whether it can update itself.
type About struct {
	Version string `json:"version"`
	// CanUpdate is false in development builds and where the app cannot
	// write where it is installed.
	CanUpdate bool `json:"canUpdate"`
}

// About returns the version of the app and whether it can update itself.
func (AppInfo) About() About {
	return About{Version: mygo.App.Version(), CanUpdate: mygo.Updater.Enabled()}
}

// CheckForUpdates checks for a new version as the user asked: the update
// window shows at once and says when the app is up to date.
func (AppInfo) CheckForUpdates() {
	updater.CheckForUpdates()
}

// setupUpdates checks for updates in the background once a day, and adds
// "Check for Updates…" to the application menu.
func setupUpdates() {
	mygo.Use(updater.Plugin)
	mygo.Bind(AppInfo{})
	mygo.App.WhenReady(func() {
		mygo.App.SetMenu(mygo.NewMenu([]*mygo.MenuItem{
			{Label: mygo.App.Name(), Submenu: []*mygo.MenuItem{
				{Role: mygo.RoleAbout},
				updater.MenuItem(),
				{Label: "Plugins…", Click: func(*mygo.MenuItem, *mygo.Window) {
					showMainWindow()
					_ = OpenPlugins.Broadcast(PluginsRequest{})
				}},
				mygo.Separator(),
				{Role: mygo.RoleServices},
				mygo.Separator(),
				{Role: mygo.RoleHide},
				{Role: mygo.RoleHideOthers},
				{Role: mygo.RoleUnhide},
				mygo.Separator(),
				{Role: mygo.RoleQuit},
			}},
			{Role: mygo.RoleEditMenu},
			{Role: mygo.RoleViewMenu},
			{Role: mygo.RoleWindowMenu},
		}))
	})
}
