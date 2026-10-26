package main

import (
	"log"
	"os"
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
	// "mcp" runs the stdio bridge to the running app, for MCP clients.
	if len(os.Args) > 1 && os.Args[1] == "mcp" {
		os.Exit(runBridge(os.Args[2:]))
	}
	// One instance at a time: on Windows and Linux, links and files opened
	// while the app runs start another, which hands them over and quits.
	if !mygo.App.RequestSingleInstanceLock() {
		return
	}
	clusters := newClusters()
	timeline := newTimeline()
	alerts := newAlerts(clusters, timeline, showMainWindow)
	kube := &Kube{clusters: clusters, timeline: timeline}
	timeline.external = clusters.snapshotChanges
	agents := newAgents(kube, clusters, showMainWindow)
	mygo.Bind(clusters)
	mygo.Bind(kube)
	mygo.Bind(&Helm{kube: kube})
	mygo.Bind(&Runbooks{clusters: clusters})
	mygo.Bind(alerts)
	mygo.Bind(agents)
	mygo.Bind(newAssistant(agents, clusters))
	plugins := newPlugins(clusters)
	mygo.Bind(plugins)
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

	// Snapshots opened from the Finder, also the one the app starts with,
	// wait for the app's folders.
	var openMu sync.Mutex
	var ready bool
	var opened, links []string
	mygo.App.OnSecondInstance(func([]string, string) { go showMainWindow() })
	// kubedesk://plugins links, from the marketplace on the website.
	mygo.App.OnOpenURL(func(u string) {
		openMu.Lock()
		defer openMu.Unlock()
		if !ready {
			links = append(links, u)
			return
		}
		go plugins.openPluginsURL(u)
	})
	mygo.App.OnOpenFile(func(path string) {
		openMu.Lock()
		defer openMu.Unlock()
		if !ready {
			opened = append(opened, path)
			return
		}
		go func() {
			clusters.openSnapshotFile(path)
			showMainWindow()
		}()
	})

	mygo.App.WhenReady(func() {
		if err := clusters.init(); err != nil {
			log.Printf("clusters: %v", err)
		}
		openMu.Lock()
		ready = true
		for _, p := range opened {
			clusters.openSnapshotFile(p)
		}
		timeline.init(clusters.dir)
		plugins.init()
		for _, u := range links {
			plugins.openPluginsURL(u)
		}
		openMu.Unlock()
		// Read the login shell's PATH early: credential plugins need it.
		go loadShellEnv()
		showMainWindow()
		alerts.start()
		agents.start()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
