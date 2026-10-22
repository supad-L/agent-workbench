package protocol

import (
	"context"
	"testing"
	"time"

	"agent-workbench/internal/connection"
)

func TestProtocolToolRoundTrips(t *testing.T) {
	for _, p := range []connection.Protocol{connection.Messages, connection.Chat, connection.Responses} {
		t.Run(string(p), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			report, err := Run(ctx, p)
			if err != nil {
				t.Fatal(err)
			}
			if !report.UsageKnown {
				t.Fatal("usage should be reported")
			}
		})
	}
}
