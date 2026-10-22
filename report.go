package main

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// Report is an incident report of an object, in Markdown.
type Report struct {
	Title    string `json:"title"`
	Markdown string `json:"markdown"`
	// Redacted counts the secrets taken out of the logs.
	Redacted int `json:"redacted"`
}

// reportLogLines is how many of the last lines of each container a report
// holds.
const reportLogLines = 80

// Report gathers what an incident needs about a pod or a workload: its
// state, events, logs and those of the previous run, its node, and the
// recent changes of its namespace. Tokens, keys and passwords in the logs
// are masked.
func (k *Kube) Report(ctx context.Context, cluster, kind, namespace, name string) (Report, error) {
	cn, err := k.clusters.conn(cluster)
	if err != nil {
		return Report{}, err
	}
	version, _ := serverVersion(ctx, cn)
	var changes []Change
	if k.timeline != nil {
		changes, _ = k.timeline.Query(TimelineQuery{Cluster: cluster, Namespace: namespace, Since: time.Now().Add(-6 * time.Hour).Format(time.RFC3339), Limit: 40})
	}
	r := &reporter{ctx: ctx, cs: cn.cs, redact: newRedactor()}
	r.header(k.clusters.name(cluster), version, kind, namespace, name)
	if err := r.object(kind, namespace, name); err != nil {
		return Report{}, err
	}
	r.recentChanges(changes)
	if items, err := impactOf(ctx, cn.cs, ImpactRequest{Action: ImpactDelete, Kind: kind, Namespace: namespace, Name: name}); err == nil && len(items) > 0 && kind != "Pod" {
		r.h2("What depends on it")
		for _, it := range items {
			r.line("- %s", it.Text)
		}
	}
	r.line("\n---\n_Made by Kube Desk on %s. Secrets in logs are masked; check before sharing._", time.Now().Format(time.RFC1123))
	return Report{Title: fmt.Sprintf("%s %s/%s", kind, namespace, name), Markdown: r.b.String(), Redacted: r.redact.count}, nil
}

type reporter struct {
	ctx    context.Context
	cs     kubernetes.Interface
	b      strings.Builder
	redact *redactor
}

func (r *reporter) line(format string, args ...any) {
	fmt.Fprintf(&r.b, format+"\n", args...)
}

func (r *reporter) h2(title string) {
	r.line("\n## %s\n", title)
}

func (r *reporter) header(cluster, version, kind, ns, name string) {
	r.line("# %s %s/%s", kind, ns, name)
	r.line("")
	r.line("- **Cluster:** %s (Kubernetes %s)", cluster, orDefault(version, "unknown"))
	r.line("- **Time:** %s", time.Now().Format(time.RFC3339))
}

func (r *reporter) object(kind, ns, name string) error {
	switch kind {
	case "Pod":
		p, err := r.cs.CoreV1().Pods(ns).Get(r.ctx, name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		r.pod(p, true)
		r.events(ns, string(p.UID), p.Name)
		return nil
	default:
		im := &impact{ctx: r.ctx, cs: r.cs, pods: map[string][]corev1.Pod{}}
		_, obj, replicas, pods, err := im.workload(kind, ns, name)
		if err != nil {
			return err
		}
		if obj == nil {
			return fmt.Errorf("reports cover pods and workloads, not %s", kind)
		}
		ready := 0
		for _, p := range pods {
			if podReady(&p) {
				ready++
			}
		}
		r.line("- **Pods:** %d ready of %d (%d wanted)", ready, len(pods), replicas)
		r.conditions(obj)
		r.events(ns, string(obj.GetUID()), name)
		// The unhealthy pods first, with their logs; at most a few.
		sort.SliceStable(pods, func(i, j int) bool { return !podReady(&pods[i]) && podReady(&pods[j]) })
		for i := range pods {
			if i == 3 {
				r.line("\n_%d more pods are left out._", len(pods)-3)
				break
			}
			r.pod(&pods[i], !podReady(&pods[i]) || i == 0)
		}
		return nil
	}
}

func podReady(p *corev1.Pod) bool {
	return ready(p) && p.Status.Phase == corev1.PodRunning
}

func (r *reporter) conditions(obj metav1.Object) {
	conds := workloadConditions(obj)
	if len(conds) > 0 {
		r.line("- **Conditions:**")
		for _, c := range conds {
			r.line("  - %s", strings.TrimSpace(c))
		}
	}
}

func (r *reporter) pod(p *corev1.Pod, logs bool) {
	r.h2("Pod " + p.Name)
	r.line("- **Phase:** %s%s", p.Status.Phase, map[bool]string{true: ", ready", false: ", not ready"}[ready(p)])
	r.line("- **Node:** %s", orDefault(p.Spec.NodeName, "not scheduled"))
	if ref := metav1.GetControllerOf(p); ref != nil {
		r.line("- **Owner:** %s %s", ref.Kind, ref.Name)
	}
	if p.Status.Message != "" {
		r.line("- **Message:** %s", p.Status.Message)
	}
	for _, c := range p.Status.Conditions {
		if c.Status != corev1.ConditionTrue && c.Message != "" {
			r.line("- **%s:** %s (%s)", c.Type, c.Message, c.Reason)
		}
	}
	r.line("")
	r.line("| Container | Image | State | Restarts | Last exit |")
	r.line("|---|---|---|---|---|")
	statuses := map[string]corev1.ContainerStatus{}
	for _, cs := range append(append([]corev1.ContainerStatus{}, p.Status.InitContainerStatuses...), p.Status.ContainerStatuses...) {
		statuses[cs.Name] = cs
	}
	for _, c := range append(append([]corev1.Container{}, p.Spec.InitContainers...), p.Spec.Containers...) {
		cs, known := statuses[c.Name]
		state := "not started"
		switch {
		case !known:
		case cs.State.Running != nil:
			state = "running"
		case cs.State.Waiting != nil:
			state = "waiting: " + cs.State.Waiting.Reason
			if cs.State.Waiting.Message != "" {
				state += " — " + truncate(cs.State.Waiting.Message, 120)
			}
		case cs.State.Terminated != nil:
			state = fmt.Sprintf("terminated: %s (%d)", cs.State.Terminated.Reason, cs.State.Terminated.ExitCode)
		}
		last := "—"
		if t := cs.LastTerminationState.Terminated; t != nil {
			last = fmt.Sprintf("%s, exit %d, %s ago", orDefault(t.Reason, "Error"), t.ExitCode, time.Since(t.FinishedAt.Time).Round(time.Second))
		}
		r.line("| %s | `%s` | %s | %d | %s |", c.Name, c.Image, mdCell(state), cs.RestartCount, last)
	}
	for _, c := range p.Spec.Containers {
		if req := resourcesLine(c.Resources); req != "none" {
			r.line("\n`%s` resources: %s", c.Name, req)
		}
	}
	r.node(p.Spec.NodeName)
	if !logs {
		return
	}
	if p.Spec.NodeName == "" {
		return // not scheduled: no logs yet
	}
	for _, c := range p.Spec.Containers {
		r.logs(p, c.Name, false)
		if statuses[c.Name].RestartCount > 0 {
			r.logs(p, c.Name, true)
		}
	}
}

func mdCell(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "|", "\\|"), "\n", " ")
}

func (r *reporter) logs(p *corev1.Pod, container string, previous bool) {
	tail := int64(reportLogLines)
	stream, err := r.cs.CoreV1().Pods(p.Namespace).GetLogs(p.Name, &corev1.PodLogOptions{Container: container, TailLines: &tail, Previous: previous, Timestamps: true}).Stream(r.ctx)
	title := fmt.Sprintf("Logs of %s, last %d lines", container, reportLogLines)
	if previous {
		title = fmt.Sprintf("Logs of %s before its last restart", container)
	}
	r.line("\n**%s**\n", title)
	if err != nil {
		r.line("_Unavailable: %s_", friendlyError(err))
		return
	}
	defer stream.Close()
	b, _ := io.ReadAll(io.LimitReader(stream, 256*1024))
	text := strings.TrimRight(r.redact.apply(string(b)), "\n")
	if text == "" {
		r.line("_No lines._")
		return
	}
	r.line("```\n%s\n```", text)
}

func (r *reporter) node(name string) {
	if name == "" {
		return
	}
	n, err := r.cs.CoreV1().Nodes().Get(r.ctx, name, metav1.GetOptions{})
	if err != nil {
		return
	}
	var bad []string
	for _, c := range n.Status.Conditions {
		if (c.Type == corev1.NodeReady) != (c.Status == corev1.ConditionTrue) {
			bad = append(bad, fmt.Sprintf("%s=%s", c.Type, c.Status))
		}
	}
	state := "healthy"
	if len(bad) > 0 {
		state = strings.Join(bad, ", ")
	}
	if n.Spec.Unschedulable {
		state += ", cordoned"
	}
	r.line("\nNode `%s`: %s", name, state)
}

func (r *reporter) events(ns, uid, name string) {
	list, err := r.cs.CoreV1().Events(ns).List(r.ctx, metav1.ListOptions{})
	if err != nil {
		return
	}
	var evs []corev1.Event
	for _, e := range list.Items {
		if string(e.InvolvedObject.UID) == uid || e.InvolvedObject.Name == name || strings.HasPrefix(e.InvolvedObject.Name, name+"-") {
			evs = append(evs, e)
		}
	}
	if len(evs) == 0 {
		return
	}
	sort.Slice(evs, func(i, j int) bool { return eventTimeOf(&evs[i]).After(eventTimeOf(&evs[j])) })
	r.h2("Events")
	r.line("| When | Type | Object | Reason | Message |")
	r.line("|---|---|---|---|---|")
	for i, e := range evs {
		if i == 25 {
			break
		}
		count := ""
		if e.Count > 1 {
			count = fmt.Sprintf(" ×%d", e.Count)
		}
		r.line("| %s ago%s | %s | %s/%s | %s | %s |", time.Since(eventTimeOf(&e)).Round(time.Second), count, e.Type,
			strings.ToLower(e.InvolvedObject.Kind), e.InvolvedObject.Name, e.Reason, mdCell(truncate(e.Message, 200)))
	}
}

func eventTimeOf(e *corev1.Event) time.Time {
	switch {
	case !e.LastTimestamp.IsZero():
		return e.LastTimestamp.Time
	case !e.EventTime.IsZero():
		return e.EventTime.Time
	}
	return e.CreationTimestamp.Time
}

func (r *reporter) recentChanges(changes []Change) {
	if len(changes) == 0 {
		return
	}
	r.h2("Recent changes in the namespace")
	for _, c := range changes {
		who := ""
		if c.Actor != "" {
			who = " by " + c.Actor
		}
		r.line("- %s · %s %s %s%s: %s", c.Time.Format("15:04:05"), c.Kind, c.Name, c.Type, who, strings.Join(c.Summary, "; "))
	}
}

// workloadConditions reads the conditions of apps and batch workloads.
func workloadConditions(obj metav1.Object) []string {
	var out []string
	add := func(t, s, reason, msg string) {
		out = append(out, strings.TrimSpace(fmt.Sprintf("%s=%s %s %s", t, s, reason, msg)))
	}
	switch o := obj.(type) {
	case *appsv1.Deployment:
		for _, c := range o.Status.Conditions {
			add(string(c.Type), string(c.Status), c.Reason, c.Message)
		}
	case *appsv1.StatefulSet:
		for _, c := range o.Status.Conditions {
			add(string(c.Type), string(c.Status), c.Reason, c.Message)
		}
	case *appsv1.DaemonSet:
		for _, c := range o.Status.Conditions {
			add(string(c.Type), string(c.Status), c.Reason, c.Message)
		}
	case *appsv1.ReplicaSet:
		for _, c := range o.Status.Conditions {
			add(string(c.Type), string(c.Status), c.Reason, c.Message)
		}
	case *batchv1.Job:
		for _, c := range o.Status.Conditions {
			add(string(c.Type), string(c.Status), c.Reason, c.Message)
		}
	}
	return out
}

// ---- Masking secrets ----

type redactor struct {
	rules []*regexp.Regexp
	count int
}

func newRedactor() *redactor {
	return &redactor{rules: []*regexp.Regexp{
		// Authorization headers and bearer tokens.
		regexp.MustCompile(`(?i)(bearer\s+)[a-z0-9._~+/=-]{12,}`),
		// JSON web tokens.
		regexp.MustCompile(`eyJ[a-zA-Z0-9_-]{8,}\.[a-zA-Z0-9_-]{8,}\.[a-zA-Z0-9_-]{8,}`),
		// AWS access keys, GitHub, Slack and Stripe tokens.
		regexp.MustCompile(`\b(AKIA|ASIA)[0-9A-Z]{16}\b`),
		regexp.MustCompile(`\b(gh[pousr]_[A-Za-z0-9]{20,}|xox[abprs]-[A-Za-z0-9-]{10,}|sk_(live|test)_[A-Za-z0-9]{10,})\b`),
		// password=..., "secret": "...", token: ...
		regexp.MustCompile(`(?i)((?:password|passwd|pwd|secret|token|api[_-]?key|access[_-]?key|client[_-]?secret)["']?\s*[:=]\s*["']?)[^\s"'&,;}]{4,}`),
		// Credentials in URLs: scheme://user:password@host.
		regexp.MustCompile(`([a-z][a-z0-9+.-]*://[^:/\s]+:)[^@/\s]+(@)`),
	}}
}

func (r *redactor) apply(s string) string {
	for i, re := range r.rules {
		s = re.ReplaceAllStringFunc(s, func(m string) string {
			r.count++
			sub := re.FindStringSubmatch(m)
			switch {
			case i == 0 || i == 4:
				return sub[1] + "[redacted]"
			case i == 5:
				return sub[1] + "[redacted]" + sub[2]
			}
			return "[redacted]"
		})
	}
	return s
}

