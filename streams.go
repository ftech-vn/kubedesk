package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/httpstream"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/client-go/transport/spdy"
)

// LogOptions selects the logs of a container.
type LogOptions struct {
	Container string `json:"container"`
	// TailLines is how many of the last lines to start with; 0 is all.
	TailLines  int  `json:"tailLines"`
	Follow     bool `json:"follow"`
	Previous   bool `json:"previous"`
	Timestamps bool `json:"timestamps"`
}

// Logs streams the logs of a container, in batches of lines, until they end
// or the page stops listening.
func (k *Kube) Logs(ctx context.Context, cluster, namespace, pod string, opts LogOptions, lines *mygo.Channel[[]string]) error {
	cn, err := k.clusters.conn(cluster)
	if err != nil {
		return err
	}
	po := &corev1.PodLogOptions{
		Container:  opts.Container,
		Follow:     opts.Follow,
		Previous:   opts.Previous,
		Timestamps: opts.Timestamps,
	}
	if opts.TailLines > 0 {
		t := int64(opts.TailLines)
		po.TailLines = &t
	}
	stream, err := openLogs(ctx, cn.cs, namespace, pod, po)
	if err != nil {
		return errors.New(friendlyError(err))
	}
	defer stream.Close()

	read := make(chan string, 1024)
	readErr := make(chan error, 1)
	go func() {
		sc := bufio.NewScanner(stream)
		sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
		for sc.Scan() {
			select {
			case read <- sc.Text():
			case <-ctx.Done():
				return
			}
		}
		readErr <- sc.Err()
		close(read)
	}()

	// Batch lines so that a burst of logs is a few messages, not thousands.
	tick := time.NewTicker(80 * time.Millisecond)
	defer tick.Stop()
	var batch []string
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		err := lines.Send(batch)
		batch = nil
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case line, ok := <-read:
			if !ok {
				if err := flush(); err != nil {
					return nil
				}
				if err := <-readErr; err != nil && ctx.Err() == nil {
					return err
				}
				return nil
			}
			batch = append(batch, line)
			if len(batch) >= 500 {
				if flush() != nil {
					return nil
				}
			}
		case <-tick.C:
			if flush() != nil {
				return nil
			}
		}
	}
}

// execSession is a shell running in a container.
type execSession struct {
	stdin  *io.PipeWriter
	resize chan remotecommand.TerminalSize
}

type sizeQueue chan remotecommand.TerminalSize

func (q sizeQueue) Next() *remotecommand.TerminalSize {
	s, ok := <-q
	if !ok {
		return nil
	}
	return &s
}

type chanWriter struct{ ch *mygo.Channel[[]byte] }

func (w chanWriter) Write(p []byte) (int, error) {
	if err := w.ch.Send(append([]byte(nil), p...)); err != nil {
		return 0, err
	}
	return len(p), nil
}

var (
	execMu       sync.Mutex
	execSessions = map[string]*execSession{}
)

// Exec runs a command in a container with a terminal, under session, and
// streams what it prints until it exits. Send it keystrokes with ExecInput
// and the terminal's size with ExecResize. An empty command starts bash, or
// sh where the image lacks bash.
func (k *Kube) Exec(ctx context.Context, cluster, namespace, pod, container string, command []string, session string, output *mygo.Channel[[]byte]) error {
	// A shell can change anything: read-only clusters have none.
	if err := k.clusters.writable(cluster); err != nil {
		return err
	}
	cn, err := k.clusters.conn(cluster)
	if err != nil {
		return err
	}
	if len(command) == 0 {
		command = []string{"/bin/sh", "-c", "if command -v bash >/dev/null 2>&1; then exec bash; else exec sh; fi"}
	}
	req := cn.cs.CoreV1().RESTClient().Post().Resource("pods").Namespace(namespace).Name(pod).SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: container,
			Command:   command,
			Stdin:     true,
			Stdout:    true,
			TTY:       true,
		}, scheme.ParameterCodec)

	spdyExec, err := remotecommand.NewSPDYExecutor(cn.cfg, "POST", req.URL())
	if err != nil {
		return err
	}
	var executor remotecommand.Executor = spdyExec
	if wsExec, err := remotecommand.NewWebSocketExecutor(cn.cfg, "GET", req.URL().String()); err == nil {
		executor, err = remotecommand.NewFallbackExecutor(wsExec, spdyExec, func(err error) bool {
			return httpstream.IsUpgradeFailure(err) || httpstream.IsHTTPSProxyError(err)
		})
		if err != nil {
			return err
		}
	}

	stdinR, stdinW := io.Pipe()
	s := &execSession{stdin: stdinW, resize: make(chan remotecommand.TerminalSize, 4)}
	execMu.Lock()
	execSessions[session] = s
	execMu.Unlock()
	defer func() {
		execMu.Lock()
		delete(execSessions, session)
		execMu.Unlock()
		stdinW.Close()
		close(s.resize)
	}()

	err = executor.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin:             stdinR,
		Stdout:            chanWriter{output},
		Tty:               true,
		TerminalSizeQueue: sizeQueue(s.resize),
	})
	if ctx.Err() != nil {
		return nil
	}
	return err
}

// ExecInput sends keystrokes to a session of Exec.
func (k *Kube) ExecInput(session, data string) error {
	execMu.Lock()
	s := execSessions[session]
	execMu.Unlock()
	if s == nil {
		return errors.New("the session ended")
	}
	_, err := s.stdin.Write([]byte(data))
	return err
}

// ExecResize tells a session of Exec the size of its terminal. It fails
// until the session has started.
func (k *Kube) ExecResize(session string, cols, rows int) error {
	execMu.Lock()
	defer execMu.Unlock()
	s := execSessions[session]
	if s == nil {
		return errors.New("no such session")
	}
	select {
	case s.resize <- remotecommand.TerminalSize{Width: uint16(cols), Height: uint16(rows)}:
	default:
	}
	return nil
}

// PortForward is a port of a pod forwarded to this machine.
type PortForward struct {
	ID         string `json:"id"`
	Cluster    string `json:"cluster"`
	Namespace  string `json:"namespace"`
	Pod        string `json:"pod"`
	RemotePort int    `json:"remotePort"`
	LocalPort  int    `json:"localPort"`
	Error      string `json:"error,omitempty"`
}

type forwarding struct {
	info PortForward
	stop chan struct{}
}

var (
	pfMu       sync.Mutex
	forwards   = map[string]*forwarding{}
	pfSequence int
)

// StartPortForward forwards a port of a pod to localPort on this machine, or
// to a free port when localPort is 0.
func (k *Kube) StartPortForward(cluster, namespace, pod string, remotePort, localPort int) (PortForward, error) {
	cn, err := k.clusters.conn(cluster)
	if err != nil {
		return PortForward{}, err
	}
	if cn.snapshot != nil {
		return PortForward{}, errSnapshot
	}
	if localPort == 0 {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return PortForward{}, err
		}
		localPort = l.Addr().(*net.TCPAddr).Port
		l.Close()
	}
	transport, upgrader, err := spdy.RoundTripperFor(cn.cfg)
	if err != nil {
		return PortForward{}, err
	}
	host, err := url.Parse(cn.cfg.Host)
	if err != nil {
		return PortForward{}, err
	}
	u := *host
	u.Path = fmt.Sprintf("%s/api/v1/namespaces/%s/pods/%s/portforward", host.Path, namespace, pod)
	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, "POST", &u)

	stop := make(chan struct{})
	ready := make(chan struct{})
	fw, err := portforward.NewOnAddresses(dialer, []string{"127.0.0.1"},
		[]string{strconv.Itoa(localPort) + ":" + strconv.Itoa(remotePort)}, stop, ready, io.Discard, io.Discard)
	if err != nil {
		return PortForward{}, err
	}
	pfMu.Lock()
	pfSequence++
	f := &forwarding{info: PortForward{
		ID: strconv.Itoa(pfSequence), Cluster: cluster, Namespace: namespace, Pod: pod,
		RemotePort: remotePort, LocalPort: localPort,
	}, stop: stop}
	forwards[f.info.ID] = f
	pfMu.Unlock()

	errCh := make(chan error, 1)
	go func() {
		err := fw.ForwardPorts()
		pfMu.Lock()
		if err != nil {
			f.info.Error = err.Error()
		} else {
			delete(forwards, f.info.ID)
		}
		pfMu.Unlock()
		errCh <- err
	}()
	select {
	case <-ready:
		return f.info, nil
	case err := <-errCh:
		pfMu.Lock()
		delete(forwards, f.info.ID)
		pfMu.Unlock()
		if err == nil {
			err = errors.New("the port forward ended")
		}
		return PortForward{}, err
	case <-time.After(15 * time.Second):
		close(stop)
		return PortForward{}, errors.New("timed out starting the port forward")
	}
}

// StopPortForward stops a port forward.
func (k *Kube) StopPortForward(id string) {
	pfMu.Lock()
	defer pfMu.Unlock()
	if f, ok := forwards[id]; ok {
		select {
		case <-f.stop:
		default:
			close(f.stop)
		}
		delete(forwards, id)
	}
}

// PortForwards returns the port forwards running.
func (k *Kube) PortForwards() []PortForward {
	pfMu.Lock()
	defer pfMu.Unlock()
	out := make([]PortForward, 0, len(forwards))
	for _, f := range forwards {
		out = append(out, f.info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// OpenURL opens a URL in the default browser.
func (k *Kube) OpenURL(u string) error {
	return mygo.Shell.OpenExternal(u)
}

// LogLine is a line of the logs of a container of a workload.
type LogLine struct {
	Pod       string `json:"pod"`
	Container string `json:"container"`
	// Time is when the container wrote the line, RFC 3339.
	Time string `json:"time"`
	Text string `json:"text"`
	// Note marks lines of the app's own, such as a pod that started.
	Note bool `json:"note,omitempty"`
}

// maxLogStreams caps the containers whose logs a workload's view follows.
const maxLogStreams = 40

// WorkloadLogs streams the logs of every pod a label selector matches in a
// namespace, interleaved by time, in batches. With Follow, it keeps watching
// the pods: those that start later join the stream from their first line.
// An empty container follows every container of the pods.
func (k *Kube) WorkloadLogs(ctx context.Context, cluster, namespace string, selector map[string]string, container string, opts LogOptions, lines *mygo.Channel[[]LogLine]) error {
	return k.workloadLogs(ctx, cluster, namespace, selector, container, opts, lines.Send)
}

func (k *Kube) workloadLogs(ctx context.Context, cluster, namespace string, selector map[string]string, container string, opts LogOptions, send func([]LogLine) error) error {
	if len(selector) == 0 {
		return errors.New("the workload has no label selector")
	}
	cn, err := k.clusters.conn(cluster)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	sel := labels.SelectorFromSet(selector).String()
	pods := cn.cs.CoreV1().Pods(namespace)
	list, err := pods.List(ctx, metav1.ListOptions{LabelSelector: sel})
	if err != nil {
		return errors.New(friendlyError(err))
	}

	out := make(chan LogLine, 4096)
	var wg sync.WaitGroup
	started := map[string]bool{}
	streams := 0
	start := func(p *corev1.Pod, initial bool) {
		names := []string{}
		for _, c := range p.Spec.Containers {
			if container == "" || c.Name == container {
				names = append(names, c.Name)
			}
		}
		for _, c := range names {
			key := p.Name + "/" + c
			if started[key] {
				continue
			}
			if streams >= maxLogStreams {
				if streams == maxLogStreams {
					streams++
					select {
					case out <- LogLine{Note: true, Text: fmt.Sprintf("Following the first %d containers only", maxLogStreams), Time: time.Now().UTC().Format(time.RFC3339Nano)}:
					default:
					}
				}
				return
			}
			started[key] = true
			streams++
			po := &corev1.PodLogOptions{Container: c, Follow: opts.Follow, Previous: opts.Previous, Timestamps: true}
			if initial && opts.TailLines > 0 {
				t := int64(opts.TailLines)
				po.TailLines = &t
			}
			wg.Add(1)
			go func(pod, c string) {
				defer wg.Done()
				streamPodLogs(ctx, cn, namespace, pod, c, po, out)
			}(p.Name, c)
		}
	}
	runnable := func(p *corev1.Pod) bool {
		return p.Status.Phase == corev1.PodRunning || p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed
	}
	for i := range list.Items {
		if runnable(&list.Items[i]) {
			start(&list.Items[i], true)
		}
	}

	done := make(chan struct{})
	if opts.Follow {
		// Pods that start later join the stream, from their first line.
		go func() {
			rv := list.ResourceVersion
			for ctx.Err() == nil {
				w, err := pods.Watch(ctx, metav1.ListOptions{LabelSelector: sel, ResourceVersion: rv})
				if err != nil {
					if !sleep(ctx, 2*time.Second) {
						return
					}
					continue
				}
				for ev := range w.ResultChan() {
					p, ok := ev.Object.(*corev1.Pod)
					if !ok {
						continue
					}
					rv = p.ResourceVersion
					if (ev.Type == watch.Added || ev.Type == watch.Modified) && runnable(p) {
						if !started[p.Name+"/"+firstContainer(p, container)] {
							select {
							case out <- LogLine{Note: true, Pod: p.Name, Text: "pod " + p.Name + " started", Time: time.Now().UTC().Format(time.RFC3339Nano)}:
							case <-ctx.Done():
								return
							}
						}
						start(p, false)
					}
				}
				w.Stop()
			}
		}()
	} else {
		go func() {
			wg.Wait()
			close(done)
		}()
	}

	// The tails of the pods arrive at once: gather them for a moment and
	// sort them, then send lines as they come, in batches.
	var batch []LogLine
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		sort.SliceStable(batch, func(i, j int) bool { return batch[i].Time < batch[j].Time })
		err := send(batch)
		batch = nil
		return err
	}
	settle := time.NewTimer(700 * time.Millisecond)
	defer settle.Stop()
	settled := false
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case l := <-out:
			batch = append(batch, l)
			if settled && len(batch) >= 1000 {
				if flush() != nil {
					return nil
				}
			}
		case <-settle.C:
			settled = true
			if flush() != nil {
				return nil
			}
		case <-tick.C:
			if settled && flush() != nil {
				return nil
			}
		case <-done:
			// Every stream ended; take what is left.
		drain:
			for {
				select {
				case l := <-out:
					batch = append(batch, l)
				default:
					break drain
				}
			}
			flush()
			return nil
		}
	}
}

func firstContainer(p *corev1.Pod, container string) string {
	if container != "" {
		return container
	}
	if len(p.Spec.Containers) > 0 {
		return p.Spec.Containers[0].Name
	}
	return ""
}

// streamPodLogs sends the lines of a container, split from their
// timestamps, until they end or ctx is done.
func streamPodLogs(ctx context.Context, cn *conn, namespace, pod, container string, po *corev1.PodLogOptions, out chan<- LogLine) {
	stream, err := openLogs(ctx, cn.cs, namespace, pod, po)
	if err != nil {
		if ctx.Err() == nil {
			select {
			case out <- LogLine{Note: true, Pod: pod, Container: container, Text: friendlyError(err), Time: time.Now().UTC().Format(time.RFC3339Nano)}:
			case <-ctx.Done():
			}
		}
		return
	}
	defer stream.Close()
	sc := bufio.NewScanner(stream)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		ts, text, ok := strings.Cut(sc.Text(), " ")
		if !ok {
			ts, text = "", sc.Text()
		}
		select {
		case out <- LogLine{Pod: pod, Container: container, Time: ts, Text: text}:
		case <-ctx.Done():
			return
		}
	}
}

// SaveText asks where to save a text, such as logs, and writes it there. It
// returns the path, or "" when the user canceled.
func (k *Kube) SaveText(ctx context.Context, name, content string) (string, error) {
	path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: mygo.CallerWindow(ctx), DefaultPath: name})
	if err != nil || path == "" {
		return "", err
	}
	return path, os.WriteFile(path, []byte(content), 0o644)
}
