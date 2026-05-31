// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/nstance-dev/nstance/internal/proto"
	"github.com/nstance-dev/nstance/pkg/health"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// SubmitHealthReports maintains a persistent stream and sends reports periodically
func (c *Client) SubmitHealthReports(ctx context.Context, reportChan <-chan health.Report) error {
	client, err := c.clientConn()
	if err != nil {
		return fmt.Errorf("failed to get client connection: %w", err)
	}

	// Establish streaming connection
	stream, err := client.SubmitHealthReport(ctx)
	if err != nil {
		return fmt.Errorf("failed to create health report stream: %w", err)
	}

	c.logger.Info("Health report stream established")

	// Send health reports from channel
	for {
		select {
		case <-ctx.Done():
			// Graceful shutdown - close stream properly
			c.logger.Info("Closing health report stream")
			if err := stream.CloseSend(); err != nil {
				c.logger.Warn("Error closing health stream", "error", err)
			}
			// Wait for server acknowledgment
			if _, err := stream.CloseAndRecv(); err != nil && err != io.EOF {
				c.logger.Warn("Error receiving close acknowledgment", "error", err)
			}
			return ctx.Err()

		case report, ok := <-reportChan:
			if !ok {
				// Channel closed, close stream gracefully
				c.logger.Info("Health report channel closed, closing stream")
				if err := stream.CloseSend(); err != nil {
					c.logger.Warn("Error closing health stream", "error", err)
				}
				if _, err := stream.CloseAndRecv(); err != nil && err != io.EOF {
					c.logger.Warn("Error receiving close acknowledgment", "error", err)
				}
				return nil
			}

			// Validate report
			if strings.TrimSpace(report.InstanceID) == "" {
				c.logger.Warn("Skipping health report with missing instance ID")
				continue
			}
			if report.Timestamp.IsZero() {
				c.logger.Warn("Skipping health report with missing timestamp")
				continue
			}

			// Serialize report
			req, err := serializeReportRequest(report)
			if err != nil {
				c.logger.Error("Failed to serialize health report", "error", err)
				continue
			}

			// Send report
			if err := stream.Send(req); err != nil {
				return fmt.Errorf("failed to send health report: %w", err)
			}

			c.logger.Debug("Health report sent",
				"count", report.Count,
				"uptime", report.Uptime)
		}
	}
}

// serializeReportRequest converts a health Report to a gRPC HealthReportRequest
func serializeReportRequest(report health.Report) (*proto.HealthReportRequest, error) {
	req := &proto.HealthReportRequest{
		InstanceId:        report.InstanceID,
		Timestamp:         timestamppb.New(report.Timestamp),
		Count:             int64(report.Count),
		Uptime:            (time.Duration(report.Uptime) * time.Second).String(),
		Files:             map[string]*proto.FileStatus{},
		Version:           report.Version,
		Started:           timestamppb.New(report.Started),
		WindowStart:       timestamppb.New(report.WindowStart),
		WindowEnd:         timestamppb.New(report.WindowEnd),
		OneMinute:         serializeMetrics(report.OneMinute),
		FiveMinutes:       serializeMetrics(report.FiveMinutes),
		FifteenMinutes:    serializeMetrics(report.FifteenMinutes),
		TerminationNotice: serializeTerminationNotice(report.TerminationNotice),
		ConfigHash:        report.ConfigHash,
	}
	for name, value := range report.Files {
		v := strings.TrimSpace(value)
		if v == "" {
			continue
		}
		lower := strings.ToLower(v)
		if strings.HasPrefix(lower, "error:") {
			errMsg := strings.TrimSpace(v[len("error:"):])
			req.Files[name] = &proto.FileStatus{
				Status: &proto.FileStatus_Error{Error: errMsg},
			}
			continue
		}
		lastModified, err := time.Parse(time.RFC3339, v)
		if err != nil {
			req.Files[name] = &proto.FileStatus{
				Status: &proto.FileStatus_Error{Error: v},
			}
			continue
		}
		req.Files[name] = &proto.FileStatus{
			Status: &proto.FileStatus_LastModified{
				LastModified: timestamppb.New(lastModified.UTC()),
			},
		}
	}

	return req, nil
}

// serializeMetrics converts a health Metrics struct to a gRPC Metrics message
func serializeMetrics(metrics health.Metrics) *proto.Metrics {
	return &proto.Metrics{
		CpuUsage:             metrics.CPUUsage,
		CpuCoreUsage:         metrics.CPUCoreUsage,
		MemoryUsage:          metrics.MemoryUsage,
		NetworkBytesSent:     metrics.NetworkBytesSent,
		NetworkBytesReceived: metrics.NetworkBytesReceived,
		DiskUsed:             metrics.DiskUsed,
		DiskInodesUsed:       metrics.DiskInodesUsed,
		DiskBytesRead:        metrics.DiskBytesRead,
		DiskBytesWritten:     metrics.DiskBytesWritten,
	}
}

// serializeTerminationNotice converts a health TerminationNotice to a gRPC TerminationNotice message
func serializeTerminationNotice(notice *health.TerminationNotice) *proto.TerminationNotice {
	if notice == nil {
		return nil
	}
	var deadline *timestamppb.Timestamp
	if !notice.Deadline.IsZero() {
		deadline = timestamppb.New(notice.Deadline)
	}
	return &proto.TerminationNotice{
		Action:   notice.Action,
		Deadline: deadline,
	}
}
