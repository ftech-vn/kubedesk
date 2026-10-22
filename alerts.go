package main

import (
	"context"
	_ "embed"
	"fmt"
	"log"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

//go:embed resources/tray.png
var trayIcon []byte

// WatchTarget is a namespace of a cluster, or a whole cluster, whose
// problems the app reports while it runs.
type WatchTarget struct {
	Cluster string `json:"cluster"`
	// Namespace is "" for the whole cluster, its nodes included.
	Namespace string `json:"namespace"`
}

// Severity says how bad an alert is.
type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityWarning  Severity = "warning"
)

// Alert is a problem of a watched object, for as long as it lasts.
type Alert struct {
	ID          string   `json:"id"`
	Cluster     string   `json:"cluster"`
	ClusterName string   `json:"clusterName"`
	Kind        string   `json:"kind"`
	Namespace   string   `json:"namespace"`
	Name        string   `json:"name"`
	Container   string   `json:"container,omitempty"`
	Reason      string   `json:"reason"`
	Message     string   `json:"message,omitempty"`
	Severity    Severity `json:"severity"`
	// Since is when the app saw the problem first.
	Since time.Time `json:"since"`
}

// AlertsState is what the watchlist knows: what it watches and the
// problems it found.
type AlertsState struct {
	Targets []WatchTarget `json:"targets"`
	Alerts  []Alert       `json:"alerts"`
	// PausedUntil is when notifications resume, RFC 3339; "" when they
	// are on.
	PausedUntil string `json:"pausedUntil"`
	// Errors are the clusters the watchlist cannot read, by ID.
	Errors map[string]string `json:"errors"`
	// Syncing lists the clusters whose first read is under way.
	Syncing []string `json:"syncing"`
}

// OpenRequest asks the page to show an object.
type OpenRequest struct {
	Cluster   string `json:"cluster"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

// AlertsChanged is sent when the watchlist or its problems change.
var AlertsChanged = mygo.NewEvent[AlertsState]("alerts:changed")

// OpenObject asks the page to show an object, from a notification or the
// menu bar.
var OpenObject = mygo.NewEvent[OpenRequest]("open-object")

// Alerts watches the namespaces the user chose, in the background, and
// tells about their problems with notifications and in the menu bar.
type Alerts struct {
	clusters *Clusters
	timeline *Timeline
	show     func()
	// client connects to a cluster; tests give a fake one.
	client func(id string) (kubernetes.Interface, error)
	// publish tells pages and the menu bar about a new state, and deliver
	// shows a notification; tests replace them.
	publish func(AlertsState)
	deliver func(mygo.NotificationOptions)

	mu          sync.Mutex
	runners     map[string]*clusterWatch
	pausedUntil time.Time
	muted       map[string]bool
	notified    map[string]time.Time
	queue       []Alert
	queueTimer  *time.Timer
	pushTimer   *time.Timer
	pending     *OpenRequest

	trayMu sync.Mutex
	tray   *mygo.Tray
}

type clusterWatch struct {
	id         string
	cancel     context.CancelFunc
	namespaces []string
	alerts     map[string]Alert
	synced     bool
	err        string
}

func newAlerts(c *Clusters, t *Timeline, show func()) *Alerts {
	a := &Alerts{
		clusters: c,
		timeline: t,
		show:     show,
		runners:  map[string]*clusterWatch{},
		muted:    map[string]bool{},
		notified: map[string]time.Time{},
	}
	a.publish = func(st AlertsState) {
		AlertsChanged.Broadcast(st)
		a.updateTray(st)
	}
	a.client = func(id string) (kubernetes.Interface, error) {
		cn, err := c.conn(id)
		if err != nil {
			return nil, err
		}
		return cn.cs, nil
	}
	a.deliver = func(opts mygo.NotificationOptions) {
		if mygo.NotificationsSupported() {
			showNotification(opts)
		}
	}
	return a
}

// start watches the saved targets; it runs once the app is ready.
func (a *Alerts) start() {
	mygo.App.OnNotificationClick(func(id string) { a.openAlert(id) })
	a.apply()
}

// apply starts and stops the watches of the clusters to match the targets.
func (a *Alerts) apply() {
	targets := a.clusters.watchTargets()
	byCluster := map[string][]string{}
	for _, t := range targets {
		byCluster[t.Cluster] = append(byCluster[t.Cluster], t.Namespace)
	}
	a.mu.Lock()
	for id, r := range a.runners {
		if ns, ok := byCluster[id]; !ok || !slices.Equal(sorted(ns), r.namespaces) {
			r.cancel()
			delete(a.runners, id)
		}
	}
	for id, ns := range byCluster {
		if _, ok := a.runners[id]; ok {
			continue
		}
		ctx, cancel := context.WithCancel(context.Background())
		r := &clusterWatch{id: id, cancel: cancel, namespaces: sorted(ns), alerts: map[string]Alert{}}
		a.runners[id] = r
		go a.run(ctx, r)
	}
	a.mu.Unlock()
	a.changed()
}

func sorted(s []string) []string {
	s = slices.Clone(s)
	sort.Strings(s)
	return slices.Compact(s)
}

// run watches the pods, deployments and, for a whole cluster, the nodes of a
// cluster until ctx is done, for problems; and what the timeline records.
func (a *Alerts) run(ctx context.Context, r *clusterWatch) {
	cs, err := a.client(r.id)
	if err != nil {
		a.setError(r, err.Error())
		return
	}
	name := a.clusters.name(r.id)
	all := slices.Contains(r.namespaces, "")
	scopes := r.namespaces
	if all {
		scopes = []string{metav1.NamespaceAll}
	}
	rec := newRecorder(a.timeline, r.id, r.namespaces)
	var synced []cache.InformerSynced
	for _, ns := range scopes {
		f := informers.NewSharedInformerFactoryWithOptions(cs, 10*time.Minute, informers.WithNamespace(ns))
		// Problems come from pods and deployments.
		for _, inf := range []cache.SharedIndexInformer{f.Core().V1().Pods().Informer(), f.Apps().V1().Deployments().Informer()} {
			a.handle(r, name, inf)
			rec.watch(inf)
			synced = append(synced, inf.HasSynced)
		}
		// The timeline records these too. They may be forbidden, as secrets
		// often are: that only leaves them out.
		for _, inf := range []cache.SharedIndexInformer{
			f.Apps().V1().StatefulSets().Informer(),
			f.Apps().V1().DaemonSets().Informer(),
			f.Core().V1().Services().Informer(),
			f.Core().V1().ConfigMaps().Informer(),
			f.Core().V1().Secrets().Informer(),
			f.Networking().V1().Ingresses().Informer(),
			f.Batch().V1().CronJobs().Informer(),
			f.Autoscaling().V2().HorizontalPodAutoscalers().Informer(),
			f.Core().V1().Events().Informer(),
		} {
			optional(inf)
			rec.watch(inf)
			synced = append(synced, syncedOrForbidden(inf))
		}
		f.Start(ctx.Done())
	}
	if all {
		f := informers.NewSharedInformerFactory(cs, 10*time.Minute)
		nodes := f.Core().V1().Nodes().Informer()
		a.handle(r, name, nodes)
		rec.watch(nodes)
		synced = append(synced, nodes.HasSynced)
		f.Start(ctx.Done())
	}
	if !cache.WaitForCacheSync(ctx.Done(), synced...) {
		return
	}
	rec.markSynced()
	a.mu.Lock()
	r.synced = true
	r.err = ""
	a.mu.Unlock()
	a.changed()
}

// optional keeps the errors of an informer the app can do without, such as
// one of secrets without the right to list them, out of the watchlist.
func optional(inf cache.SharedIndexInformer) {
	_ = inf.SetWatchErrorHandler(func(_ *cache.Reflector, err error) {
		if !strings.Contains(err.Error(), "forbidden") {
			log.Printf("timeline: %v", err)
		}
		forbidden.Store(inf, strings.Contains(err.Error(), "forbidden"))
	})
}

var forbidden sync.Map

// syncedOrForbidden waits for an informer unless the cluster forbids it.
func syncedOrForbidden(inf cache.SharedIndexInformer) cache.InformerSynced {
	return func() bool {
		if v, ok := forbidden.Load(inf); ok && v.(bool) {
			return true
		}
		return inf.HasSynced()
	}
}

func (a *Alerts) handle(r *clusterWatch, clusterName string, inf cache.SharedIndexInformer) {
	_ = inf.SetWatchErrorHandler(func(_ *cache.Reflector, err error) {
		a.setError(r, friendlyError(err))
	})
	update := func(obj any, deleted bool) {
		if d, ok := obj.(cache.DeletedFinalStateUnknown); ok {
			obj, deleted = d.Obj, true
		}
		kind, prefix, found := evaluate(r.id, clusterName, obj)
		if prefix == "" {
			return
		}
		if deleted {
			found = nil
		}
		a.replace(r, kind, prefix, found)
	}
	_, _ = inf.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj any) { update(obj, false) },
		UpdateFunc: func(_, obj any) { update(obj, false) },
		DeleteFunc: func(obj any) { update(obj, true) },
	})
}

// replace sets the alerts of one object, and notifies about new ones.
func (a *Alerts) replace(r *clusterWatch, kind, prefix string, found []Alert) {
	a.mu.Lock()
	if a.runners[r.id] != r {
		a.mu.Unlock()
		return
	}
	r.err = ""
	keep := map[string]bool{}
	changed := false
	for _, al := range found {
		keep[al.ID] = true
		if old, ok := r.alerts[al.ID]; ok {
			al.Since = old.Since
			if old.Message != al.Message {
				changed = true
			}
		} else {
			al.Since = time.Now()
			changed = true
			if r.synced {
				a.enqueue(al)
			}
		}
		r.alerts[al.ID] = al
	}
	for id := range r.alerts {
		if strings.HasPrefix(id, prefix) && !keep[id] {
			delete(r.alerts, id)
			delete(a.muted, id)
			changed = true
		}
	}
	a.mu.Unlock()
	if changed {
		a.changed()
	}
}

func (a *Alerts) setError(r *clusterWatch, msg string) {
	a.mu.Lock()
	if a.runners[r.id] == r {
		r.err = msg
	}
	a.mu.Unlock()
	a.changed()
}

// ---- What counts as a problem ----

var badWaiting = map[string]bool{
	"CrashLoopBackOff":           true,
	"ImagePullBackOff":           true,
	"ErrImagePull":               true,
	"InvalidImageName":           true,
	"CreateContainerConfigError": true,
	"CreateContainerError":       true,
	"RunContainerError":          true,
}

func alertID(cluster, kind, ns, name, container, reason string) string {
	return strings.Join([]string{cluster, kind, ns, name, container, reason}, "|")
}

func objPrefix(cluster, kind, ns, name string) string {
	return strings.Join([]string{cluster, kind, ns, name}, "|") + "|"
}

// evaluate returns the problems of an object, and the prefix of the IDs of
// its alerts.
func evaluate(cluster, clusterName string, obj any) (kind, prefix string, alerts []Alert) {
	mk := func(kind, ns, name, container, reason, msg string, sev Severity) Alert {
		return Alert{
			ID: alertID(cluster, kind, ns, name, container, reason), Cluster: cluster, ClusterName: clusterName,
			Kind: kind, Namespace: ns, Name: name, Container: container, Reason: reason,
			Message: truncate(msg, 300), Severity: sev,
		}
	}
	switch o := obj.(type) {
	case *corev1.Pod:
		kind, prefix = "Pod", objPrefix(cluster, "Pod", o.Namespace, o.Name)
		if o.DeletionTimestamp != nil {
			return kind, prefix, nil
		}
		statuses := append(slices.Clone(o.Status.InitContainerStatuses), o.Status.ContainerStatuses...)
		for _, cs := range statuses {
			last := cs.LastTerminationState.Terminated
			if w := cs.State.Waiting; w != nil && badWaiting[w.Reason] {
				reason, msg := w.Reason, w.Message
				if w.Reason == "CrashLoopBackOff" && last != nil {
					if last.Reason == "OOMKilled" {
						reason = "OOMKilled"
						msg = fmt.Sprintf("container %s ran out of memory and keeps restarting (%d restarts)", cs.Name, cs.RestartCount)
					} else {
						msg = fmt.Sprintf("container %s exited with code %d (%s), %d restarts", cs.Name, last.ExitCode, orDefault(last.Reason, "Error"), cs.RestartCount)
					}
				}
				alerts = append(alerts, mk(kind, o.Namespace, o.Name, cs.Name, reason, msg, SeverityCritical))
				continue
			}
			if last != nil && last.Reason == "OOMKilled" && time.Since(last.FinishedAt.Time) < 15*time.Minute {
				alerts = append(alerts, mk(kind, o.Namespace, o.Name, cs.Name, "OOMKilled",
					fmt.Sprintf("container %s was killed for using too much memory and restarted", cs.Name), SeverityWarning))
			}
		}
		if o.Status.Phase == corev1.PodFailed && !ownedBy(o.OwnerReferences, "Job") {
			alerts = append(alerts, mk(kind, o.Namespace, o.Name, "", "Failed", orDefault(o.Status.Message, o.Status.Reason), SeverityCritical))
		}
		if o.Status.Phase == corev1.PodPending && time.Since(o.CreationTimestamp.Time) > 2*time.Minute {
			for _, c := range o.Status.Conditions {
				if c.Type == corev1.PodScheduled && c.Status == corev1.ConditionFalse && c.Reason == "Unschedulable" {
					alerts = append(alerts, mk(kind, o.Namespace, o.Name, "", "Unschedulable", c.Message, SeverityWarning))
				}
			}
		}
	case *appsv1.Deployment:
		kind, prefix = "Deployment", objPrefix(cluster, "Deployment", o.Namespace, o.Name)
		for _, c := range o.Status.Conditions {
			if c.Type == appsv1.DeploymentProgressing && c.Status == corev1.ConditionFalse && c.Reason == "ProgressDeadlineExceeded" {
				alerts = append(alerts, mk(kind, o.Namespace, o.Name, "", "RolloutStuck", c.Message, SeverityCritical))
			}
			if c.Type == appsv1.DeploymentReplicaFailure && c.Status == corev1.ConditionTrue {
				alerts = append(alerts, mk(kind, o.Namespace, o.Name, "", "ReplicaFailure", c.Message, SeverityWarning))
			}
		}
	case *corev1.Node:
		kind, prefix = "Node", objPrefix(cluster, "Node", "", o.Name)
		for _, c := range o.Status.Conditions {
			switch {
			case c.Type == corev1.NodeReady && c.Status != corev1.ConditionTrue:
				reason := "NotReady"
				if c.Status == corev1.ConditionUnknown {
					reason = "NodeUnreachable"
				}
				alerts = append(alerts, mk(kind, "", o.Name, "", reason, c.Message, SeverityCritical))
			case (c.Type == corev1.NodeMemoryPressure || c.Type == corev1.NodeDiskPressure || c.Type == corev1.NodePIDPressure) && c.Status == corev1.ConditionTrue:
				alerts = append(alerts, mk(kind, "", o.Name, "", string(c.Type), c.Message, SeverityWarning))
			}
		}
	}
	return kind, prefix, alerts
}

func ownedBy(refs []metav1.OwnerReference, kind string) bool {
	for _, r := range refs {
		if r.Kind == kind {
			return true
		}
	}
	return false
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// ---- Notifications ----

// enqueue gathers new problems for a moment, so that a node going down
// makes one notification rather than one per pod; a.mu must be held.
func (a *Alerts) enqueue(al Alert) {
	a.queue = append(a.queue, al)
	if a.queueTimer == nil {
		a.queueTimer = time.AfterFunc(2*time.Second, a.flushQueue)
	}
}

func (a *Alerts) flushQueue() {
	a.mu.Lock()
	queue := a.queue
	a.queue, a.queueTimer = nil, nil
	paused := time.Now().Before(a.pausedUntil)
	var fresh []Alert
	for _, al := range queue {
		if a.muted[al.ID] || paused {
			continue
		}
		if last, ok := a.notified[al.ID]; ok && time.Since(last) < 30*time.Minute {
			continue
		}
		a.notified[al.ID] = time.Now()
		fresh = append(fresh, al)
	}
	a.mu.Unlock()
	if len(fresh) == 0 {
		return
	}
	byCluster := map[string][]Alert{}
	for _, al := range fresh {
		byCluster[al.Cluster] = append(byCluster[al.Cluster], al)
	}
	for _, list := range byCluster {
		if len(list) > 3 {
			first := list[0]
			a.deliver(mygo.NotificationOptions{
				ID:    "summary|" + first.Cluster,
				Group: first.Cluster,
				Title: fmt.Sprintf("%s: %d new problems", first.ClusterName, len(list)),
				Body:  summarize(list),
			})
			continue
		}
		for _, al := range list {
			where := al.Name
			if al.Namespace != "" {
				where = al.Namespace + "/" + al.Name
			}
			a.deliver(mygo.NotificationOptions{
				ID:    al.ID,
				Group: al.Cluster,
				Title: fmt.Sprintf("%s · %s", al.ClusterName, al.Reason),
				Body:  strings.TrimSpace(fmt.Sprintf("%s %s\n%s", al.Kind, where, al.Message)),
			})
		}
	}
}

func summarize(list []Alert) string {
	counts := map[string]int{}
	var reasons []string
	for _, al := range list {
		if counts[al.Reason] == 0 {
			reasons = append(reasons, al.Reason)
		}
		counts[al.Reason]++
	}
	parts := make([]string, 0, len(reasons))
	for _, r := range reasons {
		parts = append(parts, fmt.Sprintf("%d %s", counts[r], r))
	}
	return strings.Join(parts, " · ")
}

func showNotification(opts mygo.NotificationOptions) {
	go func() {
		if err := mygo.NewNotification(opts).Show(); err != nil {
			log.Printf("notification: %v", err)
		}
	}()
}

// openAlert shows the object of a clicked notification.
func (a *Alerts) openAlert(id string) {
	parts := strings.Split(id, "|")
	switch {
	case len(parts) == 2 && parts[0] == "summary":
		a.open(OpenRequest{Cluster: parts[1]})
	case len(parts) == 6:
		a.open(OpenRequest{Cluster: parts[0], Kind: parts[1], Namespace: parts[2], Name: parts[3]})
	default:
		a.show()
	}
}

func (a *Alerts) open(req OpenRequest) {
	a.mu.Lock()
	a.pending = &req
	a.mu.Unlock()
	a.show()
	OpenObject.Broadcast(req)
}

// ---- State, events and the menu bar ----

// changed tells the pages and the menu bar, at most a few times a second.
func (a *Alerts) changed() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.pushTimer == nil {
		a.pushTimer = time.AfterFunc(300*time.Millisecond, func() {
			a.mu.Lock()
			a.pushTimer = nil
			a.mu.Unlock()
			a.publish(a.State())
		})
	}
}

// State returns what the watchlist watches and the problems it found.
func (a *Alerts) State() AlertsState {
	targets := a.clusters.watchTargets()
	a.mu.Lock()
	defer a.mu.Unlock()
	st := AlertsState{Targets: targets, Alerts: []Alert{}, Errors: map[string]string{}, Syncing: []string{}}
	if time.Now().Before(a.pausedUntil) {
		st.PausedUntil = a.pausedUntil.Format(time.RFC3339)
	}
	for id, r := range a.runners {
		if r.err != "" {
			st.Errors[id] = r.err
		}
		if !r.synced {
			st.Syncing = append(st.Syncing, id)
		}
		for _, al := range r.alerts {
			if !a.muted[al.ID] {
				st.Alerts = append(st.Alerts, al)
			}
		}
	}
	sort.Slice(st.Alerts, func(i, j int) bool {
		if st.Alerts[i].Severity != st.Alerts[j].Severity {
			return st.Alerts[i].Severity == SeverityCritical
		}
		return st.Alerts[i].Since.After(st.Alerts[j].Since)
	})
	return st
}

// SetWatched starts or stops watching a namespace of a cluster; "" is the
// whole cluster.
func (a *Alerts) SetWatched(cluster, namespace string, on bool) error {
	if err := a.clusters.setWatched(cluster, namespace, on); err != nil {
		return err
	}
	a.apply()
	return nil
}

// Pause stops notifications for some minutes; the menu bar still counts
// problems.
func (a *Alerts) Pause(minutes int) {
	a.mu.Lock()
	a.pausedUntil = time.Now().Add(time.Duration(minutes) * time.Minute)
	a.mu.Unlock()
	a.changed()
}

// Resume turns notifications back on.
func (a *Alerts) Resume() {
	a.mu.Lock()
	a.pausedUntil = time.Time{}
	a.mu.Unlock()
	a.changed()
}

// Mute hides an alert until its problem goes away.
func (a *Alerts) Mute(id string) {
	a.mu.Lock()
	a.muted[id] = true
	a.mu.Unlock()
	a.changed()
}

// TestNotification shows a notification, which also asks macOS for the
// permission to show them.
func (a *Alerts) TestNotification() error {
	if !mygo.NotificationsSupported() {
		return fmt.Errorf("this build cannot show notifications")
	}
	return mygo.NewNotification(mygo.NotificationOptions{
		Title: "Kube Desk notifications are on",
		Body:  "You'll hear about crash loops, OOM kills, failed image pulls, stuck rollouts and unready nodes in the namespaces you watch.",
	}).Show()
}

// Changes returns the changes the timeline recorded in the namespaces
// watched, newest first.
func (a *Alerts) Changes(q TimelineQuery) ([]Change, error) {
	return a.timeline.Query(q)
}

// TakePendingOpen returns the object a notification asked to show before
// the page was ready, once.
func (a *Alerts) TakePendingOpen() *OpenRequest {
	a.mu.Lock()
	defer a.mu.Unlock()
	p := a.pending
	a.pending = nil
	return p
}

func (a *Alerts) updateTray(st AlertsState) {
	a.trayMu.Lock()
	defer a.trayMu.Unlock()
	{
		if len(st.Targets) == 0 {
			if a.tray != nil {
				a.tray.Destroy()
				a.tray = nil
			}
			return
		}
		if a.tray == nil {
			t, err := mygo.NewTray(mygo.TrayOptions{Icon: trayIcon, IconIsTemplate: true, ToolTip: "Kube Desk"})
			if err != nil {
				log.Printf("tray: %v", err)
				return
			}
			a.tray = t
		}
		title := ""
		if n := len(st.Alerts); n > 0 {
			title = fmt.Sprint(n)
		}
		a.tray.SetTitle(title)
		tip := "Kube Desk: all watched namespaces are healthy"
		if len(st.Alerts) > 0 {
			tip = fmt.Sprintf("Kube Desk: %d problems", len(st.Alerts))
		}
		a.tray.SetToolTip(tip)
		a.tray.SetMenu(a.trayMenu(st))
	}
}

func (a *Alerts) trayMenu(st AlertsState) *mygo.Menu {
	var items []*mygo.MenuItem
	if len(st.Alerts) == 0 {
		label := "All watched namespaces are healthy"
		if len(st.Syncing) > 0 {
			label = "Checking watched namespaces…"
		}
		items = append(items, &mygo.MenuItem{Label: label, Disabled: true})
	}
	for i, al := range st.Alerts {
		if i == 8 {
			items = append(items, &mygo.MenuItem{Label: fmt.Sprintf("%d more…", len(st.Alerts)-8), Click: func(*mygo.MenuItem, *mygo.Window) { a.show() }})
			break
		}
		icon := "🔴"
		if al.Severity == SeverityWarning {
			icon = "🟠"
		}
		where := al.Name
		if al.Namespace != "" {
			where = al.Namespace + "/" + al.Name
		}
		al := al
		items = append(items, &mygo.MenuItem{
			Label:   fmt.Sprintf("%s %s — %s", icon, truncate(where, 48), al.Reason),
			ToolTip: al.ClusterName + ": " + al.Message,
			Click: func(*mygo.MenuItem, *mygo.Window) {
				a.open(OpenRequest{Cluster: al.Cluster, Kind: al.Kind, Namespace: al.Namespace, Name: al.Name})
			},
		})
	}
	for id, msg := range st.Errors {
		items = append(items, &mygo.MenuItem{Label: "⚠️ " + a.clusters.name(id) + ": cannot watch", ToolTip: msg, Disabled: true})
	}
	items = append(items, mygo.Separator())
	var watching []*mygo.MenuItem
	for _, t := range st.Targets {
		t := t
		label := a.clusters.name(t.Cluster) + " · " + orDefault(t.Namespace, "whole cluster")
		watching = append(watching, &mygo.MenuItem{Label: label, Click: func(*mygo.MenuItem, *mygo.Window) {
			a.open(OpenRequest{Cluster: t.Cluster})
		}})
	}
	items = append(items, &mygo.MenuItem{Label: fmt.Sprintf("Watching %d", len(st.Targets)), Submenu: watching})
	if st.PausedUntil != "" {
		items = append(items, &mygo.MenuItem{Label: "Resume notifications", Click: func(*mygo.MenuItem, *mygo.Window) { a.Resume() }})
	} else {
		items = append(items, &mygo.MenuItem{Label: "Pause notifications for 1 hour", Click: func(*mygo.MenuItem, *mygo.Window) { a.Pause(60) }})
	}
	items = append(items, mygo.Separator(),
		&mygo.MenuItem{Label: "Open Kube Desk", Click: func(*mygo.MenuItem, *mygo.Window) { a.show() }},
		&mygo.MenuItem{Role: mygo.RoleQuit},
	)
	return mygo.NewMenu(items)
}
