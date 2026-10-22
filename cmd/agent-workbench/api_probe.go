package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"regexp"
	"time"

	"agent-workbench/internal/connection"
	"agent-workbench/internal/egress"
	"agent-workbench/internal/runtime/direct"
	"agent-workbench/prototypes/protocol"
)

var probeEnvironment = regexp.MustCompile(`^AWB_[A-Z0-9_]+$`)

type apiProbeConfig struct {
	Endpoint    connection.Endpoint `json:"endpoint"`
	Model       string              `json:"model"`
	AuthKind    string              `json:"auth_kind"`
	KeyEnv      string              `json:"key_env"`
	ProxyEnv    string              `json:"proxy_env,omitempty"`
	Egress      egress.Policy       `json:"egress"`
	ChatDialect string              `json:"chat_dialect,omitempty"`
}

func apiProbe(ctx context.Context, fileName string, w io.Writer) (returnErr error) {
	if fileName == "" {
		return errors.New("api-probe requires an explicit --config file; it may consume model quota")
	}
	file, err := os.Open(fileName)
	if err != nil {
		return errors.New("cannot open explicit probe configuration")
	}
	defer func() { returnErr = errors.Join(returnErr, file.Close()) }()
	decoder := json.NewDecoder(io.LimitReader(file, 65537))
	decoder.DisallowUnknownFields()
	var config apiProbeConfig
	if err := decoder.Decode(&config); err != nil {
		return errors.New("invalid probe configuration")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("probe configuration must contain exactly one JSON object")
	}
	if config.Model == "" {
		return errors.New("probe model required")
	}
	if _, err := config.Endpoint.Resolve(); err != nil {
		return err
	}
	if config.ProxyEnv != "" {
		if !probeEnvironment.MatchString(config.ProxyEnv) {
			return errors.New("proxy environment variable must have an explicit AWB_ name")
		}
		config.Egress.ProxyURL = os.Getenv(config.ProxyEnv)
	}
	client, err := egress.Client(config.Egress)
	if err != nil {
		return err
	}
	defer client.CloseIdleConnections()
	var key string
	if config.AuthKind != "none" {
		if !probeEnvironment.MatchString(config.KeyEnv) {
			return errors.New("credential environment variable must have an explicit AWB_ name")
		}
		key = os.Getenv(config.KeyEnv)
		if key == "" {
			return errors.New("explicit AWB_ credential is not configured")
		}
	}
	model, err := direct.New(direct.Config{Endpoint: config.Endpoint, Key: key, AuthKind: config.AuthKind, HTTPClient: client, ChatDialect: config.ChatDialect})
	if err != nil {
		return err
	}
	probeCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	report, err := protocol.Configured(probeCtx, model, config.Model, config.Endpoint.Protocol)
	if err != nil {
		return err
	}
	return output(w, report)
}
