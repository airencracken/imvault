// SPDX-License-Identifier: AGPL-3.0-or-later

// Package sandbox constructs explicit Bubblewrap policies. It never falls back
// to an unrestricted process when confinement was requested.
package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Base exposes runtime libraries, private temporary space and minimal devices.
// Networking is retained only for servers. Children processing uploads get a
// separate network namespace and die if their server disappears.
func Base(network bool) ([]string, error) {
	args := []string{"--unshare-user", "--unshare-pid", "--unshare-ipc", "--unshare-uts", "--cap-drop", "ALL", "--new-session"}
	if !network {
		args = append(args, "--unshare-net", "--die-with-parent")
	}
	for _, path := range []string{"/usr", "/bin", "/sbin", "/lib", "/lib64", "/etc/ld.so.cache", "/etc/alternatives"} {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return nil, err
		}
		args = append(args, "--ro-bind", path, path)
	}
	args = append(args, "--proc", "/proc", "--dev", "/dev", "--tmpfs", "/tmp", "--dir", "/app", "--dir", "/run")
	return args, nil
}

// Check verifies that the requested namespaces actually work. The caller must
// treat an error as fatal, including when kernel or service policy forbids them.
//
// The probe is the host's own true(1), wherever it lives: /usr/bin on most
// systems, /bin where busybox provides it. Both are bound read-only by Base.
func Check(ctx context.Context, binary string, args []string, env []string) error {
	probe, err := exec.LookPath("true")
	if err != nil {
		return fmt.Errorf("the Bubblewrap sandbox check needs true(1): %w", err)
	}
	if probe, err = filepath.Abs(probe); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, append(append([]string{}, args...), "--", probe)...)
	cmd.Env = env
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := RunChild(cmd); err != nil {
		return fmt.Errorf("the Bubblewrap sandbox is unavailable: %w: %s", err, strings.TrimSpace(output.String()))
	}
	return nil
}

// RuntimeEnv intentionally excludes inherited loader settings and credentials.
func RuntimeEnv() []string {
	return []string{"PATH=/usr/local/bin:/usr/bin:/bin", "LANG=C", "HOME=/tmp", "TMPDIR=/tmp"}
}

// Binary rejects the historical setuid installation mode. These policies
// require unprivileged user namespaces rather than a privileged launcher.
func Binary(path string) (string, error) {
	binary, err := exec.LookPath(path)
	if err != nil {
		return "", fmt.Errorf("the Bubblewrap launcher is required: %w", err)
	}
	binary, err = filepath.Abs(binary)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(binary)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSetuid != 0 {
		return "", fmt.Errorf("a setuid Bubblewrap is unsupported; use unprivileged user namespaces")
	}
	return binary, nil
}

// Service builds a server policy. Extra mounts are explicit operator choices;
// the default grants write access only to the existing data directory.
type Service struct {
	Prefix, DataDir, Executable string
	WriteDirs, ReadFiles        []string
	Env                         []string
	// NestedSandbox keeps user namespaces available inside the server, which
	// the media sandbox needs to build its own. Without it they are disabled,
	// so a compromised server has that much less kernel to reach.
	NestedSandbox bool
}

// forwardedEnv are the variables outside the application's prefix that the
// server still needs. The AWS SDK reads its credential chain from these when
// no explicit S3 keys are configured; TZ sets the zone logs are written in.
// Files they name, such as a shared credentials file, have to be added with
// --read-file.
var forwardedEnv = map[string]bool{
	"TZ":                                     true,
	"AWS_ACCESS_KEY_ID":                      true,
	"AWS_SECRET_ACCESS_KEY":                  true,
	"AWS_SESSION_TOKEN":                      true,
	"AWS_REGION":                             true,
	"AWS_DEFAULT_REGION":                     true,
	"AWS_PROFILE":                            true,
	"AWS_CONFIG_FILE":                        true,
	"AWS_SHARED_CREDENTIALS_FILE":            true,
	"AWS_CA_BUNDLE":                          true,
	"AWS_ENDPOINT_URL":                       true,
	"AWS_ENDPOINT_URL_S3":                    true,
	"AWS_ROLE_ARN":                           true,
	"AWS_ROLE_SESSION_NAME":                  true,
	"AWS_WEB_IDENTITY_TOKEN_FILE":            true,
	"AWS_EC2_METADATA_DISABLED":              true,
	"AWS_CONTAINER_CREDENTIALS_FULL_URI":     true,
	"AWS_CONTAINER_CREDENTIALS_RELATIVE_URI": true,
	"AWS_CONTAINER_AUTHORIZATION_TOKEN":      true,
}

func (s Service) Policy() ([]string, []string, error) {
	args, err := Base(true)
	if err != nil {
		return nil, nil, err
	}
	data, err := writableDir(s.DataDir)
	if err != nil {
		return nil, nil, err
	}
	for _, dir := range append([]string{data}, s.WriteDirs...) {
		path, err := writableDir(dir)
		if err != nil {
			return nil, nil, err
		}
		args = append(args, "--bind", path, path)
	}
	readOnly, err := readOnlyFiles(append(systemFiles(), s.ReadFiles...))
	if err != nil {
		return nil, nil, err
	}
	args = append(args, readOnly...)
	// The CA directory is needed by SMTP, OIDC, S3 and Imvault clients.
	if _, err := os.Stat("/etc/ssl/certs"); err == nil {
		args = append(args, "--ro-bind", "/etc/ssl/certs", "/etc/ssl/certs")
	}
	args = append(args, "--ro-bind", s.Executable, "/app/server", "--chdir", data)
	env := RuntimeEnv()
	if bundle := certificateBundle(); bundle != "" {
		args = append(args, "--ro-bind", bundle, "/app/ca-bundle.crt")
		env = append(env, "SSL_CERT_FILE=/app/ca-bundle.crt")
	}
	env = append(env, s.forwarded()...)
	if !s.NestedSandbox {
		args = append(args, "--disable-userns")
	}
	env = append(env, s.Prefix+"DATA_DIR="+data)
	return args, env, nil
}

// forwarded picks the variables the server receives from its launcher: its own
// settings, by prefix, and the few others named in forwardedEnv. The data
// directory is set separately, to the path actually mounted.
func (s Service) forwarded() []string {
	var env []string
	for _, entry := range s.Env {
		key, _, _ := strings.Cut(entry, "=")
		if (strings.HasPrefix(key, s.Prefix) || forwardedEnv[key]) && key != s.Prefix+"DATA_DIR" {
			env = append(env, entry)
		}
	}
	return env
}

// readOnlyFiles binds each file read-only at its own path, refusing anything
// that is not an existing regular file named by a clean absolute path.
func readOnlyFiles(paths []string) ([]string, error) {
	var args []string
	for _, path := range paths {
		if !filepath.IsAbs(path) || strings.ContainsAny(path, "\x00\r\n") {
			return nil, fmt.Errorf("sandbox read file must be an absolute path: %q", path)
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("sandbox read file must exist and be regular: %q", path)
		}
		args = append(args, "--ro-bind", path, path)
	}
	return args, nil
}

func systemFiles() []string {
	var files []string
	for _, path := range []string{"/etc/resolv.conf", "/etc/hosts", "/etc/nsswitch.conf", "/etc/localtime", "/etc/ssl/cert.pem"} {
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			files = append(files, path)
		}
	}
	return files
}

func writableDir(path string) (string, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.ContainsAny(path, "\x00\r\n") {
		return "", fmt.Errorf("sandbox writable directory must be a clean absolute path: %q", path)
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("sandbox directory must already exist: %w", err)
	}
	if err := validateWriteMount(path); err != nil {
		return "", err
	}
	if err := validateWriteMount(real); err != nil {
		return "", err
	}

	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("sandbox writable directory is not a directory: %q", path)
	}
	return path, nil
}

// Binding a bundle to a stable name also handles distributions whose CA paths
// are symlinks into directories which are otherwise hidden by the sandbox.
func certificateBundle() string {
	for _, path := range []string{"/etc/ssl/certs/ca-certificates.crt", "/etc/ssl/cert.pem", "/etc/pki/tls/certs/ca-bundle.crt", "/etc/ssl/ca-bundle.pem"} {
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			return path
		}
	}
	return ""
}

func validateWriteMount(name string) error {
	if name == "/" || name == "/var" || name == "/var/lib" || name == "/home" || name == "/root" || name == "/srv" || name == "/opt" || name == "/tmp" {
		return fmt.Errorf("sandbox writable directory is too broad: %q", name)
	}
	for _, reserved := range []string{"/usr", "/bin", "/sbin", "/lib", "/lib64", "/etc", "/proc", "/dev", "/run", "/app"} {
		if name == reserved || strings.HasPrefix(name, reserved+"/") {
			return fmt.Errorf("sandbox writable directory overlaps runtime files: %q", name)
		}
	}
	return nil
}
