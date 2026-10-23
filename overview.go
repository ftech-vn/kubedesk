package main

import (
	"context"
	"fmt"
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// Overview is a whole cluster at a glance, for the assistant and agents.
type Overview struct {
	Version     string              `json:"version,omitempty"`
	Nodes       []OverviewNode      `json:"nodes"`
	Namespaces  []OverviewNamespace `json:"namespaces"`
	NotReady    []string            `json:"workloadsNotReady,omitempty"`
	Restarting  []string            `json:"podsRestarting,omitempty"`
	Warnings    []string            `json:"warningsLastHour,omitempty"`
	MoreWarning int                 `json:"moreWarnings,omitempty"`
}

// OverviewNode is a node and its state.
type OverviewNode struct {
	Name   string `json:"name"`
	State  string `json:"state"`
	Roles  string `json:"roles,omitempty"`
	CPU    string `json:"cpu"`
	Memory string `json:"memory"`
	Pods   int    `json:"pods"`
}

// OverviewNamespace counts the pods of a namespace by status.
type OverviewNamespace struct {
	Name     string         `json:"name"`
	Pods     int            `json:"pods"`
	ByStatus map[string]int `json:"byStatus,omitempty"`
}

func clusterOverview(ctx context.Context, cs kubernetes.Interface) (Overview, error) {
	var ov Overview
	if v, err := cs.Discovery().ServerVersion(); err == nil {
		ov.Version = v.GitVersion
	}
	nodes, err := cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return ov, err
	}
	pods, err := cs.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return ov, err
	}
	nss, err := cs.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return ov, err
	}

	perNode := map[string]int{}
	byNS := map[string]*OverviewNamespace{}
	for _, ns := range nss.Items {
		byNS[ns.Name] = &OverviewNamespace{Name: ns.Name, ByStatus: map[string]int{}}
	}
	for i := range pods.Items {
		p := &pods.Items[i]
		if p.Spec.NodeName != "" && p.Status.Phase != corev1.PodSucceeded && p.Status.Phase != corev1.PodFailed {
			perNode[p.Spec.NodeName]++
		}
		n := byNS[p.Namespace]
		if n == nil {
			n = &OverviewNamespace{Name: p.Namespace, ByStatus: map[string]int{}}
			byNS[p.Namespace] = n
		}
		reason, _ := podReason(p)
		n.Pods++
		n.ByStatus[reason]++
		var restarts int32
		for _, c := range p.Status.ContainerStatuses {
			restarts += c.RestartCount
		}
		if restarts >= 5 && len(ov.Restarting) < 30 {
			ov.Restarting = append(ov.Restarting, fmt.Sprintf("%s/%s: %d restarts, %s", p.Namespace, p.Name, restarts, reason))
		}
	}
	for _, n := range nodes.Items {
		state := map[bool]string{true: "Ready", false: "NotReady"}[nodeIsReady(&n)]
		if n.Spec.Unschedulable {
			state += ", cordoned"
		}
		roles := ""
		for l := range n.Labels {
			if r, ok := cutPrefix(l, "node-role.kubernetes.io/"); ok && r != "" {
				roles += r + " "
			}
		}
		ov.Nodes = append(ov.Nodes, OverviewNode{
			Name: n.Name, State: state, Roles: trimSpace(roles),
			CPU: n.Status.Allocatable.Cpu().String(), Memory: bytesStr(n.Status.Allocatable.Memory().Value()), Pods: perNode[n.Name],
		})
	}
	for _, n := range byNS {
		ov.Namespaces = append(ov.Namespaces, *n)
	}
	sort.Slice(ov.Namespaces, func(i, j int) bool { return ov.Namespaces[i].Name < ov.Namespaces[j].Name })

	if deps, err := cs.AppsV1().Deployments("").List(ctx, metav1.ListOptions{}); err == nil {
		for _, d := range deps.Items {
			if want := ptrInt32(d.Spec.Replicas); d.Status.ReadyReplicas < want {
				ov.NotReady = append(ov.NotReady, fmt.Sprintf("deployment %s/%s: %d/%d ready", d.Namespace, d.Name, d.Status.ReadyReplicas, want))
			}
		}
	}
	if sts, err := cs.AppsV1().StatefulSets("").List(ctx, metav1.ListOptions{}); err == nil {
		for _, d := range sts.Items {
			if want := ptrInt32(d.Spec.Replicas); d.Status.ReadyReplicas < want {
				ov.NotReady = append(ov.NotReady, fmt.Sprintf("statefulset %s/%s: %d/%d ready", d.Namespace, d.Name, d.Status.ReadyReplicas, want))
			}
		}
	}
	if dss, err := cs.AppsV1().DaemonSets("").List(ctx, metav1.ListOptions{}); err == nil {
		for _, d := range dss.Items {
			if d.Status.NumberReady < d.Status.DesiredNumberScheduled {
				ov.NotReady = append(ov.NotReady, fmt.Sprintf("daemonset %s/%s: %d/%d ready", d.Namespace, d.Name, d.Status.NumberReady, d.Status.DesiredNumberScheduled))
			}
		}
	}
	if evs, err := cs.CoreV1().Events("").List(ctx, metav1.ListOptions{FieldSelector: "type=Warning"}); err == nil {
		sort.Slice(evs.Items, func(i, j int) bool { return eventTimeOf(&evs.Items[i]).After(eventTimeOf(&evs.Items[j])) })
		for _, e := range evs.Items {
			if time.Since(eventTimeOf(&e)) > time.Hour {
				continue
			}
			if len(ov.Warnings) == 25 {
				ov.MoreWarning++
				continue
			}
			ov.Warnings = append(ov.Warnings, fmt.Sprintf("%s %s/%s %s: %s", e.Namespace, e.InvolvedObject.Kind, e.InvolvedObject.Name, e.Reason, truncate(e.Message, 160)))
		}
	}
	return ov, nil
}

func cutPrefix(s, prefix string) (string, bool) {
	if len(s) >= len(prefix) && s[:len(prefix)] == prefix {
		return s[len(prefix):], true
	}
	return "", false
}

func trimSpace(s string) string {
	for len(s) > 0 && s[len(s)-1] == ' ' {
		s = s[:len(s)-1]
	}
	return s
}
