// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"imvault/internal/config"
	"imvault/internal/sandbox"
)

type mountPaths []string

func (p *mountPaths) String() string         { return fmt.Sprint([]string(*p)) }
func (p *mountPaths) Set(value string) error { *p = append(*p, value); return nil }

func runSandbox(args []string, out io.Writer) error {
	flags := commandFlags("sandbox", out)
	check := flags.Bool("check", false, "verify the sandbox without starting the server")
	bwrap := flags.String("bwrap", envOr("IMVAULT_BWRAP", "bwrap"), "Bubblewrap executable")
	var writes, reads mountPaths
	if path := os.Getenv("IMVAULT_SANDBOX_WRITE_DIR"); path != "" {
		writes = append(writes, path)
	}
	if path := os.Getenv("IMVAULT_SANDBOX_READ_FILE"); path != "" {
		reads = append(reads, path)
	}
	flags.Var(&writes, "write-dir", "additional existing writable directory (repeatable)")
	flags.Var(&reads, "read-file", "additional read-only file (repeatable)")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected sandbox arguments; use imvault sandbox --help")
	}
	if os.Geteuid() == 0 {
		return errors.New("run the sandbox as the unprivileged imvault service user")
	}
	data, err := filepath.Abs(envOr("IMVAULT_DATA_DIR", "./data"))
	if err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	environment, err := sandboxEnvironment()
	if err != nil {
		return err
	}
	policy := sandbox.Service{Prefix: "IMVAULT_", DataDir: data, Executable: executable, WriteDirs: writes, ReadFiles: reads, Env: environment}
	mounts, environment, err := policy.Policy()
	if err != nil {
		return err
	}
	binary, err := sandbox.Binary(*bwrap)
	if err != nil {
		return fmt.Errorf("Bubblewrap is required for sandbox mode: %w", err)
	}
	if err := sandbox.Check(context.Background(), binary, mounts, environment); err != nil {
		return err
	}
	if *check {
		_, err := fmt.Fprintln(out, "Bubblewrap sandbox is ready.")
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return sandbox.Run(ctx, binary, append(mounts, "--", "/app/server"), environment)
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func sandboxEnvironment() ([]string, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	overrides := map[string]string{"IMVAULT_DB": cfg.DBPath, "IMVAULT_SECRET_KEY_FILE": cfg.SecretKeyFile}
	if cfg.Storage.Driver == "disk" {
		overrides["IMVAULT_OBJECTS_DIR"] = cfg.Storage.Directory
	}
	environment := []string{}
	for _, entry := range os.Environ() {
		key := entry
		for i, c := range entry {
			if c == '=' {
				key = entry[:i]
				break
			}
		}
		if _, changed := overrides[key]; !changed {
			environment = append(environment, entry)
		}
	}
	for key, path := range overrides {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return nil, err
		}
		environment = append(environment, key+"="+absolute)
	}
	return environment, nil
}
