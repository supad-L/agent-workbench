package network

import (
	"context"
	"testing"
	"time"
)

func TestLoopbackNetworkProbe(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	report, err := Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if report.Evidence != "loopback_http_not_network_isolation" || report.StrictIsolation != "unverified_blocked" {
		t.Fatal("HTTP experiment mislabeled as strict isolation")
	}
}
