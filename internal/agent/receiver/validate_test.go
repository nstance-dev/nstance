// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package receiver

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"math/big"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestValidateFile(t *testing.T) {
	certPEM := mustCreateCertificate(t)
	pubPEM, privPEM := mustCreateKeyPairPEM(t)

	testCases := []struct {
		name     string
		filename string
		content  []byte
		wantErr  bool
	}{
		{
			name:     "empty filename",
			filename: "",
			content:  nil,
			wantErr:  true,
		},
		{
			name:     "unsupported extension",
			filename: "config.txt",
			content:  []byte("hello"),
			wantErr:  true,
		},
		{
			name:     "valid env file",
			filename: "app.env",
			content:  []byte("KEY=value\nOTHER=123"),
			wantErr:  false,
		},
		{
			name:     "invalid env file",
			filename: "bad.env",
			content:  []byte("invalid-line"),
			wantErr:  true,
		},
		{
			name:     "valid certificate",
			filename: "agent.crt",
			content:  certPEM,
			wantErr:  false,
		},
		{
			name:     "invalid certificate",
			filename: "invalid.crt",
			content:  []byte("-----BEGIN CERTIFICATE-----\nINVALID\n-----END CERTIFICATE-----"),
			wantErr:  true,
		},
		{
			name:     "valid public key",
			filename: "agent.pub",
			content:  pubPEM,
			wantErr:  false,
		},
		{
			name:     "invalid public key",
			filename: "invalid.pub",
			content:  []byte("-----BEGIN PUBLIC KEY-----\nINVALID\n-----END PUBLIC KEY-----"),
			wantErr:  true,
		},
		{
			name:     "valid private key",
			filename: "agent.key",
			content:  privPEM,
			wantErr:  false,
		},
		{
			name:     "invalid private key",
			filename: "invalid.key",
			content:  []byte("-----BEGIN PRIVATE KEY-----\nINVALID\n-----END PRIVATE KEY-----"),
			wantErr:  true,
		},
		{
			name:     "valid json file",
			filename: "config.json",
			content:  []byte(`{"enabled":true}`),
			wantErr:  false,
		},
		{
			name:     "invalid json file",
			filename: "bad.json",
			content:  []byte(`{"enabled":}`),
			wantErr:  true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var err error
			switch {
			case strings.TrimSpace(tc.filename) == "":
				err = fmt.Errorf("empty filename in payload")
			default:
				validator := DefaultValidators[strings.ToLower(filepath.Ext(tc.filename))]
				if validator == nil {
					err = fmt.Errorf("error: unsupported file extension %s", filepath.Ext(tc.filename))
				} else {
					err = validator(tc.filename, tc.content)
				}
			}
			if (err != nil) != tc.wantErr {
				t.Fatalf("validator(%q) error = %v, wantErr %t", tc.filename, err, tc.wantErr)
			}
		})
	}
}

func mustCreateCertificate(t *testing.T) []byte {
	t.Helper()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	now := time.Now().UTC()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.Add(time.Minute),
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, pub, priv)
	if err != nil {
		t.Fatalf("CreateCertificate: %v", err)
	}

	return pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certDER,
	})
}

func mustCreateKeyPairPEM(t *testing.T) (pubPEM, privPEM []byte) {
	t.Helper()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	pubBytes, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatalf("MarshalPKIXPublicKey: %v", err)
	}

	privBytes, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatalf("MarshalPKCS8PrivateKey: %v", err)
	}

	pubPEM = pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: pubBytes,
	})

	privPEM = pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: privBytes,
	})

	return pubPEM, privPEM
}
