// Command agent-workbench runs the local M1 service and explicit development probes.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"agent-workbench/contracts"
	"agent-workbench/internal/connection"
	"agent-workbench/internal/doctor"
	"agent-workbench/internal/sandbox"
	networkprobe "agent-workbench/prototypes/network"
	"agent-workbench/prototypes/protocol"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func output(w io.Writer, value any) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func run(ctx context.Context, args []string, w io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: agent-workbench serve --data-dir ABSOLUTE_PATH | doctor | protocol-probe | contracts-check | network-probe | sandbox-probe --image LOCAL_IMAGE | api-probe --config FILE")
	}
	switch args[0] {
	case "serve":
		return serve(ctx, args[1:], w)
	case "api-probe":
		flags := flag.NewFlagSet("api-probe", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		config := flags.String("config", "", "explicit test connection; may consume model quota")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 {
			return errors.New("unexpected api-probe argument")
		}
		return apiProbe(ctx, *config, w)
	case "doctor":
		if len(args) != 1 {
			return errors.New("doctor takes no arguments")
		}
		return output(w, doctor.Inspect(ctx))
	case "contracts-check":
		if len(args) != 1 {
			return errors.New("contracts-check takes no arguments")
		}
		validator, err := contracts.New()
		if err != nil {
			return err
		}
		for _, name := range contracts.Names {
			data, err := contracts.Files.ReadFile("examples/" + name + ".json")
			if err != nil {
				return err
			}
			if err := validator.Validate(name, data); err != nil {
				return err
			}
		}
		return output(w, map[string]any{"status": "passed", "evidence": "schema_and_synthetic_examples", "contracts": contracts.Names})
	case "protocol-probe":
		if len(args) != 1 {
			return errors.New("protocol-probe takes no arguments")
		}
		probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		var reports []protocol.Report
		for _, p := range []connection.Protocol{connection.Messages, connection.Chat, connection.Responses} {
			report, err := protocol.Run(probeCtx, p)
			if err != nil {
				return err
			}
			reports = append(reports, report)
		}
		return output(w, reports)
	case "sandbox-probe":
		flags := flag.NewFlagSet("sandbox-probe", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		image := flags.String("image", "", "locally installed image; no automatic pull")
		runtimeKind := flags.String("runtime", "docker", "container runtime: docker or podman")
		scratchDir := flags.String("scratch-dir", "", "existing absolute fixture parent shared with the selected runtime")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 {
			return errors.New("unexpected sandbox-probe argument")
		}
		report, probeErr := sandbox.ProbeRuntime(ctx, sandbox.ProbeOptions{Runtime: sandbox.RuntimeKind(*runtimeKind), Image: *image, ScratchDir: *scratchDir})
		if err := output(w, report); err != nil {
			return err
		}
		return probeErr
	case "network-probe":
		if len(args) != 1 {
			return errors.New("unexpected network-probe argument")
		}
		probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		report, err := networkprobe.Run(probeCtx)
		if err != nil {
			return err
		}
		return output(w, report)
	default:
		return errors.New("unknown command; production server is not implemented in M0")
	}
}
