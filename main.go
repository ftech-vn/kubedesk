package main

import (
	"log"
	"runtime"
	"sync"

	"github.com/egoist/mygo"
)

var (
	mainMu     sync.Mutex
	mainWindow *mygo.Window
)

// showMainWindow brings the window to the front, making it again when it
// was closed.
func showMainWindow() {
	mainMu.Lock()
	defer mainMu.Unlock()
	if mainWindow != nil && !mainWindow.IsDestroyed() {
		mainWindow.Show()
		mainWindow.Focus()
		return
	}
	mainWindow = mygo.NewWindow(mygo.WindowOptions{
		Title:           "Kube Desk",
		Width:           1360,
		Height:          860,
		MinWidth:        900,
		MinHeight:       560,
		BackgroundColor: "light-dark(#f7f8fa, #0d0f13)", // the page's --bg in src/styles.css
		TitleBarStyle:   mygo.TitleBarHidden,
		TitleBarHeight:  44,
		// Opens where the user left it last time.
		StateKey: "main",
		URL:      "/",
	})
}

func main() {
	clusters := newClusters()
	timeline := newTimeline()
	alerts := newAlerts(clusters, timeline, showMainWindow)
	mygo.Bind(clusters)
	mygo.Bind(&Kube{clusters: clusters, timeline: timeline})
	mygo.Bind(alerts)
	setupUpdates()

	// The watchlist keeps working with the window closed, from the menu bar.
	mygo.App.OnWindowAllClosed(func() {
		if runtime.GOOS != "darwin" && len(clusters.watchTargets()) == 0 {
			mygo.App.Quit()
		}
	})
	mygo.App.OnActivate(func(hasVisibleWindows bool) {
		if !hasVisibleWindows {
			showMainWindow()
		}
	})

	mygo.App.WhenReady(func() {
		if err := clusters.init(); err != nil {
			log.Printf("clusters: %v", err)
		}
		timeline.init(clusters.dir)
		// Read the login shell's PATH early: credential plugins need it.
		go loadShellEnv()
		showMainWindow()
		alerts.start()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
