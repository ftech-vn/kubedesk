package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// CompareRequest names an object to compare across clusters: the same
// kind, namespace and name in each.
type CompareRequest struct {
	GVR       GVR      `json:"gvr"`
	Kind      string   `json:"kind"`
	Namespace string   `json:"namespace"`
	Name      string   `json:"name"`
	Clusters  []string `json:"clusters"`
}

// CompareColumn is the object in one cluster.
type CompareColumn struct {
	Cluster     string `json:"cluster"`
	ClusterName string `json:"clusterName"`
	Found       bool   `json:"found"`
	Error       string `json:"error,omitempty"`
	// YAML is the object without what the cluster fills in, secrets masked.
	YAML string `json:"yaml,omitempty"`
}

// CompareRow is a field of the object, with its value in each column.
type CompareRow struct {
	Field   string   `json:"field"`
	Values  []string `json:"values"`
	Differs bool     `json:"differs"`
}

// CompareResult lines up the fields of an object across clusters.
type CompareResult struct {
	Columns []CompareColumn `json:"columns"`
	Rows    []CompareRow    `json:"rows"`
}

// Compare reads an object from several clusters and lines up its fields:
// images, replicas, resources, environment, ports, keys of config maps and
// secrets (their values masked). Rows whose values differ are marked.
func (k *Kube) Compare(ctx context.Context, req CompareRequest) (CompareResult, error) {
	cols := make([]CompareColumn, len(req.Clusters))
	fields := make([]map[string]string, len(req.Clusters))
	var wg sync.WaitGroup
	for i, id := range req.Clusters {
		wg.Add(1)
		go func(i int, id string) {
			defer wg.Done()
			col := CompareColumn{Cluster: id, ClusterName: k.clusters.name(id)}
			defer func() { cols[i] = col }()
			r, err := k.resource(id, req.GVR, req.Namespace)
			if err != nil {
				col.Error = err.Error()
				return
			}
			cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			o, err := r.Get(cctx, req.Name, metav1.GetOptions{})
			if err != nil {
				if !strings.Contains(err.Error(), "not found") {
					col.Error = friendlyError(err)
				}
				return
			}
			col.Found = true
			col.YAML = comparableYAML(o.Object)
			fields[i] = compareFields(o.Object)
		}(i, id)
	}
	wg.Wait()

	order := []string{}
	seen := map[string]bool{}
	for _, f := range fields {
		for k := range f {
			if !seen[k] {
				seen[k] = true
				order = append(order, k)
			}
		}
	}
	sort.SliceStable(order, func(i, j int) bool { return fieldRank(order[i]) < fieldRank(order[j]) || fieldRank(order[i]) == fieldRank(order[j]) && order[i] < order[j] })
	res := CompareResult{Columns: cols}
	for _, name := range order {
		row := CompareRow{Field: name, Values: make([]string, len(cols))}
		var first *string
		for i := range cols {
			if !cols[i].Found {
				row.Values[i] = ""
				continue
			}
			v, ok := fields[i][name]
			if !ok {
				v = "—"
			}
			row.Values[i] = v
			if first == nil {
				first = &row.Values[i]
			} else if *first != v {
				row.Differs = true
			}
		}
		res.Rows = append(res.Rows, row)
	}
	return res, nil
}

// fieldRank puts the fields people compare most first.
func fieldRank(f string) int {
	for i, p := range []string{"replicas", "image", "container ", "resources", "env ", "key ", "type", "ports", "rule ", "selector"} {
		if strings.HasPrefix(f, p) || strings.Contains(f, ": "+p) {
			return i
		}
	}
	return 100
}

// compareFields flattens an object into named fields to compare.
func compareFields(o map[string]any) map[string]string {
	out := map[string]string{}
	kind, _ := o["kind"].(string)
	switch kind {
	case "Deployment", "StatefulSet", "DaemonSet":
		var tmpl corev1.PodTemplateSpec
		var replicas *int32
		var strategy string
		switch kind {
		case "Deployment":
			var d appsv1.Deployment
			if runtime.DefaultUnstructuredConverter.FromUnstructured(o, &d) != nil {
				return genericFields(o)
			}
			tmpl, replicas, strategy = d.Spec.Template, d.Spec.Replicas, string(d.Spec.Strategy.Type)
		case "StatefulSet":
			var d appsv1.StatefulSet
			if runtime.DefaultUnstructuredConverter.FromUnstructured(o, &d) != nil {
				return genericFields(o)
			}
			tmpl, replicas, strategy = d.Spec.Template, d.Spec.Replicas, string(d.Spec.UpdateStrategy.Type)
		case "DaemonSet":
			var d appsv1.DaemonSet
			if runtime.DefaultUnstructuredConverter.FromUnstructured(o, &d) != nil {
				return genericFields(o)
			}
			tmpl, strategy = d.Spec.Template, string(d.Spec.UpdateStrategy.Type)
		}
		if replicas != nil {
			out["replicas"] = fmt.Sprint(*replicas)
		}
		out["strategy"] = strategy
		podFields(out, &tmpl.Spec)
	case "Service":
		var s corev1.Service
		if runtime.DefaultUnstructuredConverter.FromUnstructured(o, &s) != nil {
			return genericFields(o)
		}
		out["type"] = string(s.Spec.Type)
		var ports []string
		for _, p := range s.Spec.Ports {
			ports = append(ports, fmt.Sprintf("%s %d→%s/%s", p.Name, p.Port, p.TargetPort.String(), p.Protocol))
		}
		out["ports"] = strings.Join(ports, ", ")
		out["selector"] = labelsLine(s.Spec.Selector)
	case "ConfigMap":
		var c corev1.ConfigMap
		if runtime.DefaultUnstructuredConverter.FromUnstructured(o, &c) != nil {
			return genericFields(o)
		}
		for k, v := range c.Data {
			out["key "+k] = truncate(strings.ReplaceAll(v, "\n", "⏎"), 120)
		}
		for k, v := range c.BinaryData {
			out["key "+k] = fmt.Sprintf("binary, %d bytes", len(v))
		}
	case "Secret":
		var s corev1.Secret
		if runtime.DefaultUnstructuredConverter.FromUnstructured(o, &s) != nil {
			return genericFields(o)
		}
		out["type"] = string(s.Type)
		for k, h := range hashes(s.Data) {
			out["key "+k] = "secret " + h
		}
	case "Ingress":
		var in networkingv1.Ingress
		if runtime.DefaultUnstructuredConverter.FromUnstructured(o, &in) != nil {
			return genericFields(o)
		}
		if in.Spec.IngressClassName != nil {
			out["class"] = *in.Spec.IngressClassName
		}
		for _, r := range in.Spec.Rules {
			if r.HTTP == nil {
				continue
			}
			for _, p := range r.HTTP.Paths {
				target := ""
				if p.Backend.Service != nil {
					target = fmt.Sprintf("%s:%s", p.Backend.Service.Name, portOf(p.Backend.Service.Port))
				}
				out["rule "+orDefault(r.Host, "*")+orDefault(p.Path, "/")] = target
			}
		}
		var tls []string
		for _, t := range in.Spec.TLS {
			tls = append(tls, strings.Join(t.Hosts, ",")+" ← "+t.SecretName)
		}
		if len(tls) > 0 {
			out["tls"] = strings.Join(tls, "; ")
		}
	default:
		return genericFields(o)
	}
	if l, ok := o["metadata"].(map[string]any)["labels"].(map[string]any); ok {
		for k, v := range l {
			out["label "+k] = fmt.Sprint(v)
		}
	}
	return out
}

func portOf(p networkingv1.ServiceBackendPort) string {
	if p.Name != "" {
		return p.Name
	}
	return fmt.Sprint(p.Number)
}

func labelsLine(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+m[k])
	}
	return strings.Join(parts, ", ")
}

func podFields(out map[string]string, spec *corev1.PodSpec) {
	for _, c := range append(append([]corev1.Container{}, spec.InitContainers...), spec.Containers...) {
		p := c.Name + ": "
		out[p+"image"] = c.Image
		out[p+"resources"] = resourcesLine(c.Resources)
		if len(c.Command)+len(c.Args) > 0 {
			out[p+"command"] = truncate(strings.Join(append(append([]string{}, c.Command...), c.Args...), " "), 160)
		}
		var ports []string
		for _, port := range c.Ports {
			ports = append(ports, fmt.Sprintf("%d/%s", port.ContainerPort, port.Protocol))
		}
		if len(ports) > 0 {
			out[p+"ports"] = strings.Join(ports, ", ")
		}
		for _, e := range c.Env {
			switch {
			case e.ValueFrom == nil:
				out[p+"env "+e.Name] = truncate(e.Value, 120)
			case e.ValueFrom.SecretKeyRef != nil:
				out[p+"env "+e.Name] = "from secret " + e.ValueFrom.SecretKeyRef.Name + "/" + e.ValueFrom.SecretKeyRef.Key
			case e.ValueFrom.ConfigMapKeyRef != nil:
				out[p+"env "+e.Name] = "from config map " + e.ValueFrom.ConfigMapKeyRef.Name + "/" + e.ValueFrom.ConfigMapKeyRef.Key
			case e.ValueFrom.FieldRef != nil:
				out[p+"env "+e.Name] = "from field " + e.ValueFrom.FieldRef.FieldPath
			default:
				out[p+"env "+e.Name] = "from a resource"
			}
		}
		for _, e := range c.EnvFrom {
			switch {
			case e.SecretRef != nil:
				out[p+"env from secret "+e.SecretRef.Name] = "yes"
			case e.ConfigMapRef != nil:
				out[p+"env from config map "+e.ConfigMapRef.Name] = "yes"
			}
		}
	}
	if spec.ServiceAccountName != "" {
		out["service account"] = spec.ServiceAccountName
	}
	if len(spec.NodeSelector) > 0 {
		out["node selector"] = labelsLine(spec.NodeSelector)
	}
	var vols []string
	for _, v := range spec.Volumes {
		src := "other"
		switch {
		case v.ConfigMap != nil:
			src = "config map " + v.ConfigMap.Name
		case v.Secret != nil:
			src = "secret " + v.Secret.SecretName
		case v.PersistentVolumeClaim != nil:
			src = "claim " + v.PersistentVolumeClaim.ClaimName
		case v.EmptyDir != nil:
			src = "emptyDir"
		case v.Projected != nil:
			src = "projected"
		}
		vols = append(vols, v.Name+"←"+src)
	}
	if len(vols) > 0 {
		sort.Strings(vols)
		out["volumes"] = strings.Join(vols, ", ")
	}
}

// genericFields flattens the spec of any object into dotted paths.
func genericFields(o map[string]any) map[string]string {
	out := map[string]string{}
	var walk func(path string, v any)
	walk = func(path string, v any) {
		if len(out) > 300 {
			return
		}
		switch t := v.(type) {
		case map[string]any:
			for k, x := range t {
				walk(strings.TrimPrefix(path+"."+k, "."), x)
			}
		case []any:
			for i, x := range t {
				walk(fmt.Sprintf("%s[%d]", path, i), x)
			}
		default:
			out[path] = truncate(fmt.Sprint(t), 120)
		}
	}
	for _, top := range []string{"spec", "data"} {
		if v, ok := o[top]; ok {
			if top == "data" && o["kind"] == "Secret" {
				continue
			}
			walk(top, v)
		}
	}
	return out
}
