// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package secrets

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

type fakeParameterStoreClient struct {
	getInput    *ssm.GetParameterInput
	getOutput   *ssm.GetParameterOutput
	getErr      error
	putInput    *ssm.PutParameterInput
	putCalls    int
	putErr      error
	deleteInput *ssm.DeleteParameterInput
	deleteErr   error
}

func (f *fakeParameterStoreClient) GetParameter(_ context.Context, input *ssm.GetParameterInput, _ ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	f.getInput = input
	return f.getOutput, f.getErr
}

func (f *fakeParameterStoreClient) PutParameter(_ context.Context, input *ssm.PutParameterInput, _ ...func(*ssm.Options)) (*ssm.PutParameterOutput, error) {
	f.putInput = input
	f.putCalls++
	return &ssm.PutParameterOutput{}, f.putErr
}

func (f *fakeParameterStoreClient) DeleteParameter(_ context.Context, input *ssm.DeleteParameterInput, _ ...func(*ssm.Options)) (*ssm.DeleteParameterOutput, error) {
	f.deleteInput = input
	return &ssm.DeleteParameterOutput{}, f.deleteErr
}

func TestAWSParameterStoreGet(t *testing.T) {
	client := &fakeParameterStoreClient{getOutput: &ssm.GetParameterOutput{Parameter: &types.Parameter{
		Type:  types.ParameterTypeSecureString,
		Value: aws.String("secret value"),
	}}}
	store := NewAWSParameterStore(client, "/nstance/test/")

	got, err := store.Get(context.Background(), "ca.key")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if string(got) != "secret value" {
		t.Fatalf("Get() = %q, want %q", got, "secret value")
	}
	if got := aws.ToString(client.getInput.Name); got != "/nstance/test/ca.key" {
		t.Errorf("GetParameter name = %q", got)
	}
	if !aws.ToBool(client.getInput.WithDecryption) {
		t.Error("GetParameter WithDecryption = false")
	}
}

func TestAWSParameterStoreGetNotFound(t *testing.T) {
	client := &fakeParameterStoreClient{getErr: &types.ParameterNotFound{}}
	store := NewAWSParameterStore(client, "/nstance/test/")

	_, err := store.Get(context.Background(), "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get() error = %v, want ErrNotFound", err)
	}
}

func TestAWSParameterStoreSet(t *testing.T) {
	client := &fakeParameterStoreClient{}
	store := NewAWSParameterStore(client, "/nstance/test/")

	if err := store.Set(context.Background(), "ca.key", []byte("secret value")); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if got := aws.ToString(client.putInput.Name); got != "/nstance/test/ca.key" {
		t.Errorf("PutParameter name = %q", got)
	}
	if got := aws.ToString(client.putInput.Value); got != "secret value" {
		t.Errorf("PutParameter value = %q", got)
	}
	if client.putInput.Type != types.ParameterTypeSecureString {
		t.Errorf("PutParameter type = %q", client.putInput.Type)
	}
	if client.putInput.Tier != types.ParameterTierStandard {
		t.Errorf("PutParameter tier = %q", client.putInput.Tier)
	}
	if !aws.ToBool(client.putInput.Overwrite) {
		t.Error("PutParameter Overwrite = false")
	}
}

func TestAWSParameterStoreSetRejectsUnsupportedValues(t *testing.T) {
	for _, test := range []struct {
		name string
		data []byte
	}{
		{name: "invalid UTF-8", data: []byte{0xff}},
		{name: "too large", data: bytes.Repeat([]byte("x"), standardParameterValueLimit+1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &fakeParameterStoreClient{}
			store := NewAWSParameterStore(client, "/nstance/test/")
			if err := store.Set(context.Background(), "value", test.data); err == nil {
				t.Fatal("Set() error = nil")
			}
			if client.putCalls != 0 {
				t.Fatalf("PutParameter calls = %d, want 0", client.putCalls)
			}
		})
	}
}

func TestAWSParameterStoreDeleteNotFound(t *testing.T) {
	client := &fakeParameterStoreClient{deleteErr: &types.ParameterNotFound{}}
	store := NewAWSParameterStore(client, "/nstance/test/")

	if err := store.Delete(context.Background(), "missing"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if got := aws.ToString(client.deleteInput.Name); got != "/nstance/test/missing" {
		t.Errorf("DeleteParameter name = %q", got)
	}
}

func TestLoadKeyFromAWSParameterStore(t *testing.T) {
	key := "0123456789abcdefghijklmnopqrstuv"
	client := &fakeParameterStoreClient{getOutput: &ssm.GetParameterOutput{Parameter: &types.Parameter{
		Type:  types.ParameterTypeSecureString,
		Value: aws.String(key),
	}}}

	got, err := loadKey(context.Background(), KeyConfig{
		Provider: "aws-parameter-store",
		Source:   "/nstance/test/encryption-key",
	}, nil, client, nil)
	if err != nil {
		t.Fatalf("loadKey() error = %v", err)
	}
	if string(got) != key {
		t.Fatalf("loadKey() = %q", got)
	}
	if !aws.ToBool(client.getInput.WithDecryption) {
		t.Error("GetParameter WithDecryption = false")
	}
}
