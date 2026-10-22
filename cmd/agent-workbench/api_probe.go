package main

import (
	"agent-workbench/internal/apiconfig"
	"context"
	"errors"
	"io"
	"os"
)

type apiProbeConfig = apiconfig.Config

func readAPIConfig(fileName string) (config apiconfig.Config, returnErr error) {
	if fileName == "" {
		return config, errors.New("api-probe requires an explicit --config file; it may consume model quota")
	}
	if fileName == "-" {
		return apiconfig.Load(os.Stdin)
	}
	file, err := os.Open(fileName)
	if err != nil {
		return config, errors.New("cannot open explicit probe configuration")
	}
	defer func() { returnErr = errors.Join(returnErr, file.Close()) }()
	return apiconfig.Load(file)
}
func apiProbe(ctx context.Context, fileName string, w io.Writer) error {
	config, err := readAPIConfig(fileName)
	if err != nil {
		return err
	}
	report, err := config.Run(ctx, os.Getenv)
	if err != nil {
		return err
	}
	return output(w, report)
}
