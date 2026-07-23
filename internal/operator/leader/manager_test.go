// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package leader

import (
	"context"
	"testing"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type notifyingClient struct {
	client.Client
	gets chan struct{}
}

// Get records the request before delegating it to the wrapped client.
func (c *notifyingClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	select {
	case c.gets <- struct{}{}:
	default:
	}
	return c.Client.Get(ctx, key, obj, opts...)
}

// TestRefreshKubeconfigRunsUntilCanceled verifies periodic refresh and context cancellation.
func TestRefreshKubeconfigRunsUntilCanceled(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}

	const (
		namespace   = "test"
		clusterName = "cluster--tenant"
	)
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      clusterName + "-kubeconfig",
			Namespace: namespace,
			Annotations: map[string]string{
				"nstance.dev/token-expiry": time.Now().Add(time.Hour).Format(time.RFC3339),
			},
		},
	}
	gets := make(chan struct{}, 1)
	m := &Manager{
		client: &notifyingClient{
			Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build(),
			gets:   gets,
		},
		namespace:   namespace,
		clusterName: clusterName,
	}

	ctx, cancel := context.WithCancel(context.Background())
	ticks := make(chan time.Time, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		m.refreshKubeconfig(ctx, logr.Discard(), ticks)
	}()

	ticks <- time.Now()
	select {
	case <-gets:
	case <-time.After(time.Second):
		t.Fatal("kubeconfig refresh did not run")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("kubeconfig refresh did not stop after cancellation")
	}
}
