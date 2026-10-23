package main

import (
	"context"
	"sync"
	"time"

	authorizationv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// AccessCheck asks whether the user may do something in a cluster, as
// kubectl auth can-i does.
type AccessCheck struct {
	Verb        string `json:"verb"`
	Group       string `json:"group"`
	Resource    string `json:"resource"`
	Subresource string `json:"subresource,omitempty"`
	// Namespace is the namespace, or "" for all of them and for
	// cluster-scoped resources.
	Namespace string `json:"namespace,omitempty"`
}

// accessTTL is how long an answer is trusted: roles change rarely, and the
// cluster refuses what is not allowed anyway.
const accessTTL = 2 * time.Minute

type accessAnswer struct {
	allowed bool
	at      time.Time
}

// accessCache remembers the answers of each cluster.
type accessCache struct {
	mu      sync.Mutex
	answers map[string]map[AccessCheck]accessAnswer
}

var access = &accessCache{answers: map[string]map[AccessCheck]accessAnswer{}}

// Can answers access checks for the user of a cluster, in order. When the
// cluster cannot tell, the answer is yes: the buttons the page enables
// still fail with the cluster's own error.
func (k *Kube) Can(ctx context.Context, cluster string, checks []AccessCheck) ([]bool, error) {
	cn, err := k.clusters.conn(cluster)
	if err != nil {
		return nil, err
	}
	return access.can(ctx, cluster, cn.cs, checks, time.Now()), nil
}

func (a *accessCache) can(ctx context.Context, cluster string, cs kubernetes.Interface, checks []AccessCheck, now time.Time) []bool {
	out := make([]bool, len(checks))
	var missing []int
	a.mu.Lock()
	known := a.answers[cluster]
	for i, c := range checks {
		if ans, ok := known[c]; ok && now.Sub(ans.at) < accessTTL {
			out[i] = ans.allowed
		} else {
			missing = append(missing, i)
		}
	}
	a.mu.Unlock()
	if len(missing) == 0 {
		return out
	}

	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for _, i := range missing {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			c := checks[i]
			review, err := cs.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, &authorizationv1.SelfSubjectAccessReview{
				Spec: authorizationv1.SelfSubjectAccessReviewSpec{ResourceAttributes: &authorizationv1.ResourceAttributes{
					Verb: c.Verb, Group: c.Group, Resource: c.Resource, Subresource: c.Subresource, Namespace: c.Namespace,
				}},
			}, metav1.CreateOptions{})
			// A cluster that cannot answer is not taken as a no, and the
			// answer is not kept.
			if err != nil {
				out[i] = true
				return
			}
			out[i] = review.Status.Allowed
			a.mu.Lock()
			if a.answers[cluster] == nil {
				a.answers[cluster] = map[AccessCheck]accessAnswer{}
			}
			a.answers[cluster][c] = accessAnswer{allowed: review.Status.Allowed, at: now}
			a.mu.Unlock()
		}(i)
	}
	wg.Wait()
	return out
}

// forget drops what is known of a cluster's access, as when it reconnects
// with other credentials.
func (a *accessCache) forget(cluster string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.answers, cluster)
}
