// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package files

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

// LoadPEM reads and parses a PEM file, returning the parsed cryptographic object.
// Returns nil if the file does not exist and required is false.
// Supported types: ed25519.PublicKey, ed25519.PrivateKey, *x509.Certificate
func LoadPEM(logger *slog.Logger, path string, file string, required bool) (any, error) {
	if path == "" {
		return nil, fmt.Errorf("file directory path is invalid: %s", path)
	}
	if file == "" {
		return nil, fmt.Errorf("filename is invalid: %s", file)
	}
	filePath := filepath.Join(path, file)

	fh, err := os.Open(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			if required {
				return nil, fmt.Errorf("file does not exist: %s", filePath)
			}
			return nil, nil
		}
		return nil, fmt.Errorf("error opening file %s: %s", filePath, err)
	}
	defer func() {
		_ = fh.Close()
	}()

	pemData, err := io.ReadAll(fh)
	if err != nil {
		return nil, fmt.Errorf("error reading file %s: %s", filePath, err)
	}
	return DecodePem(pemData, filePath, true)
}

// WritePEM writes PEM-encoded data to a file after validating it.
func WritePEM(path string, file string, data string, mode os.FileMode) error {
	if path == "" {
		return fmt.Errorf("file directory path is invalid")
	}
	if file == "" {
		return fmt.Errorf("filename is invalid")
	}
	if mode == 0 {
		return fmt.Errorf("file mode %d is invalid", mode)
	}
	filePath := filepath.Join(path, file)

	// Validate PEM data
	_, err := DecodePem([]byte(data), filePath, false)
	if err != nil {
		return err
	}

	fh, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("error creating file: %s", err)
	}
	defer func() {
		_ = fh.Close()
	}()

	_, err = fh.WriteString(data)
	if err != nil {
		return fmt.Errorf("error writing to file: %s", err)
	}

	return nil
}

// DecodePem decodes and parses PEM data, returning the parsed cryptographic object
// For validation only (when returnParsed is false), returns nil on success
// For loading (when returnParsed is true), returns the parsed key/certificate
func DecodePem(pemData []byte, filePath string, returnParsed bool) (any, error) {
	block, _ := pem.Decode(pemData)
	if block == nil {
		return nil, fmt.Errorf("invalid PEM file: %s", filePath)
	}

	switch block.Type {
	case "PRIVATE KEY":
		privateKey, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("error parsing private key %s: %s", filePath, err)
		}
		if returnParsed {
			return privateKey, nil
		}
		return nil, nil
	case "PUBLIC KEY":
		publicKey, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("error parsing public key %s: %s", filePath, err)
		}
		if returnParsed {
			return publicKey, nil
		}
		return nil, nil
	case "CERTIFICATE":
		certificate, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("error parsing certificate %s: %s", filePath, err)
		}
		if returnParsed {
			return certificate, nil
		}
		return nil, nil
	default:
		return nil, fmt.Errorf("unsupported PEM header %s: %s", filePath, block.Type)
	}
}

// EncodePem encodes an ed25519 public key, ed25519 private key, and/or x509
// certificate into PEM format. Note that return values are nil if corresponding
// input is nil.
func EncodePem(publicKeyRaw ed25519.PublicKey, privateKeyRaw ed25519.PrivateKey, certificateRaw *x509.Certificate) (publicKey []byte, privateKey []byte, certificate []byte, err error) {
	// Encode the public key in PEM format using PKIX
	if publicKeyRaw != nil {
		var publicKeyBytes []byte
		publicKeyBytes, err = x509.MarshalPKIXPublicKey(publicKeyRaw)
		if err != nil {
			return
		}
		publicKey = pem.EncodeToMemory(&pem.Block{
			Type:  "PUBLIC KEY",
			Bytes: publicKeyBytes,
		})
	}

	// Encode the private key in PEM format using PKCS#8
	if privateKeyRaw != nil {
		var privateKeyBytes []byte
		privateKeyBytes, err = x509.MarshalPKCS8PrivateKey(privateKeyRaw)
		if err != nil {
			return
		}
		privateKey = pem.EncodeToMemory(&pem.Block{
			Type:  "PRIVATE KEY",
			Bytes: privateKeyBytes,
		})
	}

	// Encode the certificate in PEM format using
	if certificateRaw != nil {
		certificate = pem.EncodeToMemory(&pem.Block{
			Type:  "CERTIFICATE",
			Bytes: certificateRaw.Raw,
		})
	}
	return
}
