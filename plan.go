package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/yaml"
)

// ObjectDiff is what applying a document would do to one object.
type ObjectDiff struct {
	Kind      string `json:"kind"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	// Action is "create", "update" or "unchanged".
	Action string `json:"action"`
	Before string `json:"before"`
	After  string `json:"after"`
}

// PlanApply works out, without changing anything, what Apply would do to
// each object of a document: the YAML of each object now and after.
// Secret values are masked.
func (k *Kube) PlanApply(ctx context.Context, cluster, manifest, namespace string) ([]ObjectDiff, error) {
	cn, err := k.clusters.conn(cluster)
	if err != nil {
		return nil, err
	}
	dec := utilyaml.NewYAMLOrJSONDecoder(strings.NewReader(manifest), 4096)
	var out []ObjectDiff
	for {
		var raw map[string]any
		if err := dec.Decode(&raw); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return out, fmt.Errorf("invalid YAML: %w", err)
		}
		if len(raw) == 0 {
			continue
		}
		obj := &unstructured.Unstructured{Object: raw}
		gvk := obj.GroupVersionKind()
		if gvk.Kind == "" || gvk.Version == "" {
			return out, errors.New("every object needs apiVersion and kind")
		}
		mapping, err := cn.mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
		if err != nil {
			cn.mapper.Reset()
			if mapping, err = cn.mapper.RESTMapping(gvk.GroupKind(), gvk.Version); err != nil {
				return out, err
			}
		}
		var r dynamic.ResourceInterface = cn.dyn.Resource(mapping.Resource)
		if mapping.Scope.Name() == "namespace" {
			if obj.GetNamespace() == "" {
				obj.SetNamespace(orDefault(namespace, "default"))
			}
			r = cn.dyn.Resource(mapping.Resource).Namespace(obj.GetNamespace())
		}
		d := ObjectDiff{Kind: gvk.Kind, Namespace: obj.GetNamespace(), Name: obj.GetName(), After: comparableYAML(obj.Object)}
		existing, err := r.Get(ctx, obj.GetName(), metav1.GetOptions{})
		switch {
		case apierrors.IsNotFound(err):
			d.Action = "create"
		case err != nil:
			return out, err
		default:
			d.Before = comparableYAML(existing.Object)
			d.Action = "update"
			if d.Before == d.After {
				d.Action = "unchanged"
			}
		}
		out = append(out, d)
	}
	if len(out) == 0 {
		return nil, errors.New("the document has no objects")
	}
	return out, nil
}

// comparableYAML is the YAML of an object without what the cluster fills in
// (status, identifiers, managed fields), so that what someone wrote compares
// with what the cluster holds. Secret values are masked.
func comparableYAML(o map[string]any) string {
	c := (&unstructured.Unstructured{Object: o}).DeepCopy().Object
	unstructured.RemoveNestedField(c, "status")
	for _, f := range []string{"managedFields", "resourceVersion", "uid", "creationTimestamp", "generation", "selfLink"} {
		unstructured.RemoveNestedField(c, "metadata", f)
	}
	if ann, ok, _ := unstructured.NestedStringMap(c, "metadata", "annotations"); ok {
		delete(ann, "kubectl.kubernetes.io/last-applied-configuration")
		delete(ann, "deployment.kubernetes.io/revision")
		if len(ann) == 0 {
			unstructured.RemoveNestedField(c, "metadata", "annotations")
		} else {
			_ = unstructured.SetNestedStringMap(c, ann, "metadata", "annotations")
		}
	}
	maskSecret(c)
	b, err := yaml.Marshal(c)
	if err != nil {
		return ""
	}
	return string(b)
}

// maskSecret replaces the values of a secret with a fingerprint, so that
// changed values show as changed without their content.
func maskSecret(o map[string]any) {
	if o["kind"] != "Secret" {
		return
	}
	for _, field := range []string{"data", "stringData"} {
		m, ok := o[field].(map[string]any)
		if !ok {
			continue
		}
		masked := map[string]any{}
		for k, v := range m {
			s := fmt.Sprint(v)
			if field == "data" {
				masked[k] = fmt.Sprintf("<secret %s>", hashes(map[string][]byte{k: []byte(s)})[k])
			} else {
				masked[k] = fmt.Sprintf("<secret, %d characters>", len(s))
			}
		}
		o[field] = masked
	}
}

func fromUnstructured(o map[string]any, into any) error {
	return runtime.DefaultUnstructuredConverter.FromUnstructured(o, into)
}
