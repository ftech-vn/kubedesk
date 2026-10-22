package main

import (
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func reasons(alerts []Alert) string {
	var r []string
	for _, a := range alerts {
		r = append(r, a.Reason)
	}
	return strings.Join(r, ",")
}

func pod(statuses ...corev1.ContainerStatus) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "web-1", Namespace: "shop", CreationTimestamp: metav1.Now()},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: statuses},
	}
}

func TestPodAlerts(t *testing.T) {
	crash := corev1.ContainerStatus{
		Name: "app", RestartCount: 4,
		State:                corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}},
		LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 1, Reason: "Error"}},
	}
	oomLoop := crash
	oomLoop.LastTerminationState = corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 137, Reason: "OOMKilled"}}
	oomOnce := corev1.ContainerStatus{
		Name:                 "app",
		State:                corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
		LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{Reason: "OOMKilled", FinishedAt: metav1.NewTime(time.Now().Add(-time.Minute))}},
	}
	oomLongAgo := oomOnce
	oomLongAgo.LastTerminationState = corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{Reason: "OOMKilled", FinishedAt: metav1.NewTime(time.Now().Add(-time.Hour))}}
	pull := corev1.ContainerStatus{Name: "app", State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff", Message: "not found"}}}
	starting := corev1.ContainerStatus{Name: "app", State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ContainerCreating"}}}

	for _, tc := range []struct {
		name string
		pod  *corev1.Pod
		want string
	}{
		{"crash loop", pod(crash), "CrashLoopBackOff"},
		{"crash loop from OOM", pod(oomLoop), "OOMKilled"},
		{"recent OOM kill", pod(oomOnce), "OOMKilled"},
		{"old OOM kill", pod(oomLongAgo), ""},
		{"image pull", pod(pull), "ImagePullBackOff"},
		{"starting", pod(starting), ""},
	} {
		_, prefix, got := evaluate("c1", "prod", tc.pod)
		if reasons(got) != tc.want {
			t.Errorf("%s: alerts %q, want %q", tc.name, reasons(got), tc.want)
		}
		if prefix != "c1|Pod|shop|web-1|" {
			t.Errorf("%s: prefix %q", tc.name, prefix)
		}
		for _, a := range got {
			if !strings.HasPrefix(a.ID, prefix) {
				t.Errorf("%s: id %q lacks the prefix %q", tc.name, a.ID, prefix)
			}
		}
	}

	terminating := pod(crash)
	now := metav1.Now()
	terminating.DeletionTimestamp = &now
	if _, _, got := evaluate("c1", "prod", terminating); len(got) != 0 {
		t.Errorf("terminating pod: %q, want none", reasons(got))
	}

	failedJob := pod()
	failedJob.Status.Phase = corev1.PodFailed
	failedJob.OwnerReferences = []metav1.OwnerReference{{Kind: "Job", Name: "j"}}
	if _, _, got := evaluate("c1", "prod", failedJob); len(got) != 0 {
		t.Errorf("failed pod of a job: %q, want none (the job reports it)", reasons(got))
	}
}

func TestNodeAndDeploymentAlerts(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n1"}, Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{
		{Type: corev1.NodeReady, Status: corev1.ConditionUnknown},
		{Type: corev1.NodeDiskPressure, Status: corev1.ConditionTrue},
	}}}
	if _, _, got := evaluate("c1", "prod", node); reasons(got) != "NodeUnreachable,DiskPressure" {
		t.Errorf("node: %q", reasons(got))
	}
	dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "shop"}, Status: appsv1.DeploymentStatus{Conditions: []appsv1.DeploymentCondition{
		{Type: appsv1.DeploymentProgressing, Status: corev1.ConditionFalse, Reason: "ProgressDeadlineExceeded"},
	}}}
	if _, _, got := evaluate("c1", "prod", dep); reasons(got) != "RolloutStuck" {
		t.Errorf("deployment: %q", reasons(got))
	}
}

func TestSummarize(t *testing.T) {
	got := summarize([]Alert{{Reason: "NotReady"}, {Reason: "CrashLoopBackOff"}, {Reason: "CrashLoopBackOff"}})
	if got != "1 NotReady · 2 CrashLoopBackOff" {
		t.Errorf("summarize = %q", got)
	}
}

func TestWatchTargets(t *testing.T) {
	c := newTestClusters(t)
	if err := c.setWatched("a", "shop", true); err != nil {
		t.Fatal(err)
	}
	_ = c.setWatched("a", "shop", true)
	_ = c.setWatched("a", "", true)
	if got := c.watchTargets(); len(got) != 2 {
		t.Fatalf("targets = %v, want 2", got)
	}
	_ = c.setWatched("a", "shop", false)
	if got := c.watchTargets(); len(got) != 1 || got[0].Namespace != "" {
		t.Fatalf("targets = %v", got)
	}
}
