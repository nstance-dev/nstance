// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// TestBootstrapResourcesAndSelectiveNonceDeletion verifies bootstrap resource ownership.
func TestBootstrapResourcesAndSelectiveNonceDeletion(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	objects := []client.Object{
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "ca", Namespace: "ns"}, Data: map[string]string{"ca.crt": "ca-data"}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "nonce", Namespace: "ns"}, Data: map[string][]byte{"nonce.jwt": []byte("jwt-data")}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "managed", Namespace: "ns"}, Data: map[string][]byte{"tls.crt": []byte("keep")}},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
	loader := NewLoader(c, "ns")
	if got, err := loader.LoadShardCA(ctx, "ca"); err != nil || string(got) != "ca-data" {
		t.Fatalf("LoadShardCA = %q, %v", got, err)
	}
	if got, err := loader.LoadNonce(ctx, "nonce"); err != nil || got != "jwt-data" {
		t.Fatalf("LoadNonce = %q, %v", got, err)
	}
	if err := loader.DeleteNonce(ctx, "nonce"); err != nil {
		t.Fatal(err)
	}
	if err := loader.DeleteNonce(ctx, "nonce"); err != nil {
		t.Fatalf("idempotent delete: %v", err)
	}
	for _, object := range []client.Object{&corev1.ConfigMap{}, &corev1.Secret{}} {
		name := "ca"
		if _, ok := object.(*corev1.Secret); ok {
			name = "managed"
		}
		if err := c.Get(ctx, types.NamespacedName{Name: name, Namespace: "ns"}, object); err != nil {
			t.Fatalf("bootstrap deletion removed %s: %v", name, err)
		}
	}
}

// TestLoadOrGenerateKeypairIsLazyAndPersistent verifies keypair reuse across loads.
func TestLoadOrGenerateKeypairIsLazyAndPersistent(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	loader := NewLoader(c, "ns")
	first, generated, err := loader.LoadOrGenerateKeypair(ctx, "key")
	if err != nil || !generated {
		t.Fatalf("first load = generated %v, err %v", generated, err)
	}
	second, generated, err := loader.LoadOrGenerateKeypair(ctx, "key")
	if err != nil || generated || !first.Equal(second) {
		t.Fatalf("second load = generated %v, err %v, equal %v", generated, err, first.Equal(second))
	}
}

// TestCompleteRegistrationKeepsNonceWhenVerificationFails verifies deletion ordering.
func TestCompleteRegistrationKeepsNonceWhenVerificationFails(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	nonce := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "nonce", Namespace: "ns"}, Data: map[string][]byte{"nonce.jwt": []byte("jwt")}}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(nonce).Build()
	loader := NewLoader(c, "ns")
	if _, _, err := loader.CompleteRegistration(ctx, "cert", "nonce", []byte("invalid certificate"), []byte("invalid key"), []byte("invalid CA")); err == nil {
		t.Fatal("invalid certificate unexpectedly verified")
	}
	if err := c.Get(ctx, types.NamespacedName{Name: "nonce", Namespace: "ns"}, &corev1.Secret{}); err != nil {
		t.Fatalf("nonce deleted before certificate verification: %v", err)
	}
	if err := c.Get(ctx, types.NamespacedName{Name: "cert", Namespace: "ns"}, &corev1.Secret{}); err != nil {
		t.Fatalf("certificate was not stored before verification: %v", err)
	}
}
