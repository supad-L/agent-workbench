// awb-network-probe is a static container diagnostic, with no provider operations.
package main

import (
	"agent-workbench/internal/netcheck"
	"context"
	"os"
	"time"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if netcheck.Serve(ctx, os.Stdin, os.Stdout) != nil {
		os.Exit(1)
	}
}
