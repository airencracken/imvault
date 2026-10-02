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

	"github.com/airencracken/comfylib/sandbox"

	"imvault/internal/config"
)

// forwardedEnv are the variables outside IMVAULT_ that the confined server
// still needs. The AWS SDK reads its credential chain from these when no
// explicit S3 keys are configured; TZ sets the zone logs are written in.
// Files they name, such as a shared credentials file, have to be added with
// --read-file.
var forwardedEnv = []string{
	"TZ",
	"AWS_ACCESS_KEY_ID",
	"AWS_SECRET_ACCESS_KEY",
	"AWS_SESSION_TOKEN",
	"AWS_REGION",
	"AWS_DEFAULT_REGION",
	"AWS_PROFILE",
	"AWS_CONFIG_FILE",
	"AWS_SHARED_CREDENTIALS_FILE",
	"AWS_CA_BUNDLE",
	"AWS_ENDPOINT_URL",
	"AWS_ENDPOINT_URL_S3",
	"AWS_ROLE_ARN",
	"AWS_ROLE_SESSION_NAME",
	"AWS_WEB_IDENTITY_TOKEN_FILE",
	"AWS_EC2_METADATA_DISABLED",
	"AWS_CONTAINER_CREDENTIALS_FULL_URI",
	"AWS_CONTAINER_CREDENTIALS_RELATIVE_URI",
	"AWS_CONTAINER_AUTHORIZATION_TOKEN",
}

// sandboxProbe is what the sandbox check runs inside the namespaces: the
// confined server itself, asking only for help, which needs no configuration,
// data or network and exits 0. It proves the policy can start this binary
// rather than a host utility that might live elsewhere.
var sandboxProbe = []string{"/app/server", "--help"}

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
	environment, nested, err := sandboxEnvironment()
	if err != nil {
		return err
	}
	mounts, environment, err := servicePolicy(data, executable, writes, reads, environment, nested).Policy()
	if err != nil {
		return err
	}
	binary, err := sandbox.Binary(*bwrap)
	if err != nil {
		return fmt.Errorf("sandbox mode needs Bubblewrap: %w", err)
	}
	if err := sandbox.Check(context.Background(), binary, mounts, environment, sandboxProbe...); err != nil {
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

// servicePolicy is the server's confinement: its own data directory and the
// operator's extra mounts writable, its IMVAULT_ settings and forwardedEnv
// passed in, and user namespaces kept only when it builds media sandboxes.
func servicePolicy(data, executable string, writes, reads, environment []string, nested bool) sandbox.Service {
	return sandbox.Service{
		Prefix:        "IMVAULT_",
		DataDir:       data,
		Executable:    executable,
		WriteDirs:     writes,
		ReadFiles:     reads,
		Env:           environment,
		ForwardEnv:    forwardedEnv,
		NestedSandbox: nested,
	}
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

// sandboxEnvironment returns the server's environment with its paths made
// absolute, and whether the server will need to build media sandboxes of its
// own inside this one.
func sandboxEnvironment() ([]string, bool, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, false, err
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
			return nil, false, err
		}
		environment = append(environment, key+"="+absolute)
	}
	return environment, cfg.MediaSandbox, nil
}
