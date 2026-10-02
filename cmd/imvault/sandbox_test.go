// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The policy Imvault builds forwards its own settings, the AWS credential
// chain and the time zone, and nothing else from the launcher, and never puts
// a secret among the Bubblewrap arguments.
func TestServicePolicyForwardsTheCredentialChainAndKeepsSecretsOutOfArguments(t *testing.T) {
	data := t.TempDir()
	environment := []string{
		"IMVAULT_SMTP_PASSWORD=secret-value", "IMVAULT_DATA_DIR=wrong", "AWS_SECRET_ACCESS_KEY=chain-secret",
		"AWS_WEB_IDENTITY_TOKEN_FILE=/run/token", "AWS_CONTAINER_AUTHORIZATION_TOKEN=container-token", "TZ=Europe/London",
		"GITHUB_TOKEN=unrelated", "AWS_SECRET_ACCESS_KEYX=lookalike", "LD_PRELOAD=host-loader", "TMPDIR=/host/tmp",
	}
	args, env, err := servicePolicy(data, "/usr/bin/true", nil, nil, environment, false).Policy()
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, "\n")
	if strings.Contains(joined, "secret") || strings.Contains(joined, "container-token") {
		t.Fatalf("a secret reached the Bubblewrap arguments:\n%s", joined)
	}
	if !slices.Contains(args, "--disable-userns") {
		t.Fatal("user namespaces stay available although no media sandbox is needed")
	}
	joined = strings.Join(env, "\n")
	for _, required := range []string{"IMVAULT_SMTP_PASSWORD=secret-value", "IMVAULT_DATA_DIR=" + data, "AWS_SECRET_ACCESS_KEY=chain-secret",
		"AWS_WEB_IDENTITY_TOKEN_FILE=/run/token", "AWS_CONTAINER_AUTHORIZATION_TOKEN=container-token", "TZ=Europe/London", "TMPDIR=/tmp"} {
		if !strings.Contains(joined, required) {
			t.Errorf("environment is missing %s", required)
		}
	}
	for _, forbidden := range []string{"GITHUB_TOKEN", "lookalike", "LD_PRELOAD", "=wrong", "/host/tmp"} {
		if strings.Contains(joined, forbidden) {
			t.Errorf("environment leaks %s", forbidden)
		}
	}
}

// Every variable the AWS SDK's default chain reads is forwarded; dropping one
// breaks S3 storage only inside the sandbox, which is hard to diagnose.
func TestTheForwardedListCoversTheAWSChain(t *testing.T) {
	for _, name := range []string{
		"TZ", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN", "AWS_REGION", "AWS_DEFAULT_REGION",
		"AWS_PROFILE", "AWS_CONFIG_FILE", "AWS_SHARED_CREDENTIALS_FILE", "AWS_CA_BUNDLE", "AWS_ENDPOINT_URL",
		"AWS_ENDPOINT_URL_S3", "AWS_ROLE_ARN", "AWS_ROLE_SESSION_NAME", "AWS_WEB_IDENTITY_TOKEN_FILE",
		"AWS_EC2_METADATA_DISABLED", "AWS_CONTAINER_CREDENTIALS_FULL_URI", "AWS_CONTAINER_CREDENTIALS_RELATIVE_URI",
		"AWS_CONTAINER_AUTHORIZATION_TOKEN",
	} {
		if !slices.Contains(forwardedEnv, name) {
			t.Errorf("%s is not forwarded", name)
		}
	}
	if len(forwardedEnv) != 19 {
		t.Errorf("forwardedEnv has %d names; review additions against the list above", len(forwardedEnv))
	}
}

func TestMediaSandboxKeepsUserNamespacesInTheServer(t *testing.T) {
	args, _, err := servicePolicy(t.TempDir(), "/usr/bin/true", nil, nil, nil, true).Policy()
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(args, "--disable-userns") {
		t.Fatal("the media sandbox could not be built inside this policy")
	}
}

func TestOperatorMountsReachThePolicy(t *testing.T) {
	root := t.TempDir()
	data, extra, file := filepath.Join(root, "data"), filepath.Join(root, "objects"), filepath.Join(root, "credentials")
	for _, dir := range []string{data, extra} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	args, _, err := servicePolicy(data, "/usr/bin/true", []string{extra}, []string{file}, nil, false).Policy()
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, "\n")
	if !strings.Contains(joined, "--bind\n"+extra+"\n"+extra) || !strings.Contains(joined, "--ro-bind\n"+file+"\n"+file) {
		t.Fatalf("operator mounts are missing:\n%s", joined)
	}
	for _, unsafe := range [][2][]string{{{"/"}, nil}, {{"relative"}, nil}, {nil, {"relative.json"}}, {nil, {root}}} {
		if _, _, err := servicePolicy(data, "/usr/bin/true", unsafe[0], unsafe[1], nil, false).Policy(); err == nil {
			t.Errorf("unsafe mounts %v were accepted", unsafe)
		}
	}
}

// The sandbox check runs the confined binary with sandboxProbe, so that
// command line must succeed without configuration, data or network.
func TestTheSandboxProbeIsACommandThatSucceeds(t *testing.T) {
	if sandboxProbe[0] != "/app/server" {
		t.Fatalf("the probe runs %q, not the bound server", sandboxProbe[0])
	}
	t.Setenv("IMVAULT_DATA_DIR", filepath.Join(t.TempDir(), "missing"))
	t.Setenv("IMVAULT_TRUSTED_PROXIES", "not an address")
	if err := runCommand(sandboxProbe[1:], nil, io.Discard); err != nil {
		t.Fatalf("imvault %s failed: %v", strings.Join(sandboxProbe[1:], " "), err)
	}
	if _, err := os.Stat(os.Getenv("IMVAULT_DATA_DIR")); !os.IsNotExist(err) {
		t.Fatal("the probe touched the data directory")
	}
}
