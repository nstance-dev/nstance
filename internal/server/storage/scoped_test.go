// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"bytes"
	"context"
	"io"
	"testing"
)

func TestScopedStorage_PrefixesKeys(t *testing.T) {
	mock := NewMock()
	defer func() { _ = mock.Cleanup() }()

	scoped := NewScopedStorage(mock, "test/")
	ctx := context.Background()

	if err := scoped.Put(ctx, "mykey", []byte("myvalue")); err != nil {
		t.Fatalf("Put failed: %v", err)
	}

	exists, err := mock.Exists(ctx, "test/mykey")
	if err != nil {
		t.Fatalf("Exists failed: %v", err)
	}
	if !exists {
		t.Error("Expected key 'test/mykey' to exist in underlying storage")
	}

	data, _, err := scoped.Get(ctx, "mykey")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if string(data) != "myvalue" {
		t.Errorf("Expected 'myvalue', got '%s'", string(data))
	}
}

func TestScopedStorage_PutIfMatch(t *testing.T) {
	mock := NewMock()
	defer func() { _ = mock.Cleanup() }()

	scoped := NewScopedStorage(mock, "scope/")
	ctx := context.Background()

	if err := scoped.PutIfMatch(ctx, "key1", []byte("value1"), ""); err != nil {
		t.Fatalf("PutIfMatch (create) failed: %v", err)
	}

	exists, _ := mock.Exists(ctx, "scope/key1")
	if !exists {
		t.Error("Expected 'scope/key1' to exist in underlying storage")
	}

	_, etag, err := scoped.Get(ctx, "key1")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if err := scoped.PutIfMatch(ctx, "key1", []byte("value2"), etag); err != nil {
		t.Fatalf("PutIfMatch (update) failed: %v", err)
	}

	if err := scoped.PutIfMatch(ctx, "key1", []byte("value3"), "wrongetag"); err != ErrPrecondition {
		t.Errorf("Expected ErrPrecondition, got: %v", err)
	}
}

func TestScopedStorage_Delete(t *testing.T) {
	mock := NewMock()
	defer func() { _ = mock.Cleanup() }()

	scoped := NewScopedStorage(mock, "prefix/")
	ctx := context.Background()

	_ = scoped.Put(ctx, "todelete", []byte("data"))

	if err := scoped.Delete(ctx, "todelete"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	exists, _ := mock.Exists(ctx, "prefix/todelete")
	if exists {
		t.Error("Expected key to be deleted from underlying storage")
	}
}

func TestScopedStorage_Exists(t *testing.T) {
	mock := NewMock()
	defer func() { _ = mock.Cleanup() }()

	scoped := NewScopedStorage(mock, "ex/")
	ctx := context.Background()

	exists, err := scoped.Exists(ctx, "noexist")
	if err != nil {
		t.Fatalf("Exists failed: %v", err)
	}
	if exists {
		t.Error("Expected key to not exist")
	}

	_ = mock.Put(ctx, "ex/exists", []byte("data"))

	exists, err = scoped.Exists(ctx, "exists")
	if err != nil {
		t.Fatalf("Exists failed: %v", err)
	}
	if !exists {
		t.Error("Expected key to exist")
	}
}

func TestScopedStorage_ListStripsPrefix(t *testing.T) {
	mock := NewMock()
	defer func() { _ = mock.Cleanup() }()

	scoped := NewScopedStorage(mock, "cluster/")
	ctx := context.Background()

	_ = mock.Put(ctx, "cluster/secret/ca.key", []byte("key1"))
	_ = mock.Put(ctx, "cluster/secret/ca.crt", []byte("key2"))
	_ = mock.Put(ctx, "cluster/config/settings.json", []byte("config"))
	_ = mock.Put(ctx, "other/data", []byte("unrelated"))

	keys, err := scoped.List(ctx, "secret/")
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	if len(keys) != 2 {
		t.Errorf("Expected 2 keys, got %d: %v", len(keys), keys)
	}

	for _, key := range keys {
		if key != "secret/ca.key" && key != "secret/ca.crt" {
			t.Errorf("Unexpected key: %s", key)
		}
	}

	allKeys, err := scoped.List(ctx, "")
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	if len(allKeys) != 3 {
		t.Errorf("Expected 3 keys, got %d: %v", len(allKeys), allKeys)
	}
}

func TestScopedStorage_GetMetadataUpdatesKey(t *testing.T) {
	mock := NewMock()
	defer func() { _ = mock.Cleanup() }()

	scoped := NewScopedStorage(mock, "meta/")
	ctx := context.Background()

	_ = scoped.Put(ctx, "file.txt", []byte("content"))

	meta, err := scoped.GetMetadata(ctx, "file.txt")
	if err != nil {
		t.Fatalf("GetMetadata failed: %v", err)
	}

	if meta.Key != "file.txt" {
		t.Errorf("Expected Key to be 'file.txt', got '%s'", meta.Key)
	}

	if meta.Size != 7 {
		t.Errorf("Expected Size 7, got %d", meta.Size)
	}
}

func TestScopedStorage_GetStream(t *testing.T) {
	mock := NewMock()
	defer func() { _ = mock.Cleanup() }()

	scoped := NewScopedStorage(mock, "stream/")
	ctx := context.Background()

	_ = mock.Put(ctx, "stream/file.bin", []byte("binary data"))

	reader, err := scoped.GetStream(ctx, "file.bin")
	if err != nil {
		t.Fatalf("GetStream failed: %v", err)
	}
	defer func() { _ = reader.Close() }()

	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}

	if string(data) != "binary data" {
		t.Errorf("Expected 'binary data', got '%s'", string(data))
	}
}

func TestScopedStorage_PutStream(t *testing.T) {
	mock := NewMock()
	defer func() { _ = mock.Cleanup() }()

	scoped := NewScopedStorage(mock, "streams/")
	ctx := context.Background()

	content := []byte("streamed content")
	reader := bytes.NewReader(content)

	if err := scoped.PutStream(ctx, "uploaded.bin", reader, int64(len(content))); err != nil {
		t.Fatalf("PutStream failed: %v", err)
	}

	data, _, err := mock.Get(ctx, "streams/uploaded.bin")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if string(data) != "streamed content" {
		t.Errorf("Expected 'streamed content', got '%s'", string(data))
	}
}

func TestScopedStorage_EmptyPrefix(t *testing.T) {
	mock := NewMock()
	defer func() { _ = mock.Cleanup() }()

	scoped := NewScopedStorage(mock, "")
	ctx := context.Background()

	if err := scoped.Put(ctx, "direct/key", []byte("value")); err != nil {
		t.Fatalf("Put failed: %v", err)
	}

	exists, _ := mock.Exists(ctx, "direct/key")
	if !exists {
		t.Error("Expected key to exist directly in underlying storage")
	}

	keys, err := scoped.List(ctx, "direct/")
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	if len(keys) != 1 || keys[0] != "direct/key" {
		t.Errorf("Expected ['direct/key'], got %v", keys)
	}
}

func TestScopedStorage_Prefix(t *testing.T) {
	mock := NewMock()
	defer func() { _ = mock.Cleanup() }()

	scoped := NewScopedStorage(mock, "myprefix/").(*ScopedStorage)
	if scoped.Prefix() != "myprefix/" {
		t.Errorf("Expected 'myprefix/', got '%s'", scoped.Prefix())
	}
}
