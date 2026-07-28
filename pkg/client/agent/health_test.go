// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"testing"
	"time"

	"github.com/nstance-dev/nstance/pkg/health"
)

// TestSerializeReportRequest verifies report metadata and termination notice conversion.
func TestSerializeReportRequest(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	deadline := now.Add(2 * time.Minute)
	report := health.Report{
		InstanceID:        "ins",
		Version:           "2.0.0",
		Timestamp:         now,
		Count:             4,
		Uptime:            65,
		TerminationNotice: &health.TerminationNotice{Action: "terminate", Deadline: deadline},
		ConfigHash:        "sha256:config",
	}
	request, err := serializeReportRequest(report)
	if err != nil {
		t.Fatal(err)
	}
	if request.InstanceId != "ins" || request.Version != "2.0.0" || request.Count != 4 || request.Uptime != "1m5s" || request.ConfigHash != "sha256:config" || !request.Timestamp.AsTime().Equal(now) {
		t.Fatalf("report metadata mismatch: %#v", request)
	}
	if request.TerminationNotice.Action != "terminate" || !request.TerminationNotice.Deadline.AsTime().Equal(deadline) {
		t.Fatalf("termination notice mismatch: %#v", request.TerminationNotice)
	}
}

// TestSerializeMetrics verifies every health metric is converted.
func TestSerializeMetrics(t *testing.T) {
	cpu, memory := 7.0, 50.0
	interfaceError, ebpfError, conntrackError := "interface error", "eBPF error", "conntrack error"
	conntrackCount, conntrackMax := uint64(3), uint64(1024)
	metrics := serializeMetrics(health.Metrics{
		CPUUsage:     &cpu,
		CPUCoreUsage: []float64{6, 8},
		MemoryUsage:  &memory,
		NetworkInterface: &health.InterfaceMetrics{
			InterfaceName: "eth0",
			RXBytes:       10,
			TXBytes:       20,
			RXPackets:     30,
			TXPackets:     40,
			RXDrops:       50,
			TXDrops:       60,
		},
		NetworkInterfaceError: &interfaceError,
		EBPFCounters:          map[uint32]uint64{443: 2, 6443: 0},
		EBPFError:             &ebpfError,
		ConntrackCount:        &conntrackCount,
		ConntrackMax:          &conntrackMax,
		ConntrackError:        &conntrackError,
	})
	if metrics.GetCpuUsage() != cpu || len(metrics.CpuCoreUsage) != 2 || metrics.CpuCoreUsage[0] != 6 || metrics.CpuCoreUsage[1] != 8 || metrics.GetMemoryUsage() != memory {
		t.Fatalf("host metrics mismatch: %#v", metrics)
	}
	interfaceMetrics := metrics.NetworkInterface
	if interfaceMetrics.InterfaceName != "eth0" || interfaceMetrics.RxBytes != 10 || interfaceMetrics.TxBytes != 20 || interfaceMetrics.RxPackets != 30 || interfaceMetrics.TxPackets != 40 || interfaceMetrics.RxDrops != 50 || interfaceMetrics.TxDrops != 60 {
		t.Fatalf("interface metrics mismatch: %#v", interfaceMetrics)
	}
	if metrics.GetNetworkInterfaceError() != interfaceError || metrics.EbpfCounters[443] != 2 || metrics.EbpfCounters[6443] != 0 || metrics.GetEbpfError() != ebpfError || metrics.GetConntrackCount() != conntrackCount || metrics.GetConntrackMax() != conntrackMax || metrics.GetConntrackError() != conntrackError {
		t.Fatalf("optional metrics mismatch: %#v", metrics)
	}
}

// TestSerializeReportFiles verifies timestamps, errors, and omitted empty statuses.
func TestSerializeReportFiles(t *testing.T) {
	report := health.Report{
		Timestamp: time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC),
		Files: map[string]string{
			"valid":   "2026-07-27T11:00:00Z",
			"failed":  "error: permission denied",
			"invalid": "not a timestamp",
			"empty":   "",
		},
	}
	request, err := serializeReportRequest(report)
	if err != nil {
		t.Fatal(err)
	}
	if len(request.Files) != 3 || request.Files["failed"].GetError() != "permission denied" || request.Files["invalid"].GetError() != "not a timestamp" || !request.Files["valid"].GetLastModified().AsTime().Equal(time.Date(2026, 7, 27, 11, 0, 0, 0, time.UTC)) {
		t.Fatalf("file status mismatch: %#v", request.Files)
	}
}

// TestSerializeOptionalValues verifies absent health values remain absent.
func TestSerializeOptionalValues(t *testing.T) {
	if serializeInterfaceMetrics(nil) != nil {
		t.Fatal("nil interface metrics were serialized")
	}
	if serializeTerminationNotice(nil) != nil {
		t.Fatal("nil termination notice was serialized")
	}
	notice := serializeTerminationNotice(&health.TerminationNotice{Action: "terminate"})
	if notice.Action != "terminate" || notice.Deadline != nil {
		t.Fatalf("termination notice = %#v", notice)
	}
	metrics := serializeMetrics(health.Metrics{})
	if metrics.NetworkInterface != nil || metrics.NetworkInterfaceError != nil || metrics.EbpfCounters != nil || metrics.EbpfError != nil || metrics.ConntrackCount != nil || metrics.ConntrackMax != nil || metrics.ConntrackError != nil {
		t.Fatalf("empty metrics gained values: %#v", metrics)
	}
}
