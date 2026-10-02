// SPDX-License-Identifier: AGPL-3.0-or-later

package media

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"imvault/internal/testutil"
)

func TestRequestedSandboxCannotFallBack(t *testing.T) {
	for _, paths := range [][3]string{{"/missing", "/missing", "bwrap"}, {"/usr/bin/true", "/usr/bin/true", "/missing"}, {"/usr/bin/true", "/usr/bin/true", "/usr/bin/false"}} {
		if _, err := ConfiguredVideo(t.Context(), paths[0], paths[1], true, paths[2]); err == nil {
			t.Fatalf("unsafe fallback for %v", paths)
		}
	}
	if f, err := ConfiguredVideo(t.Context(), "/missing", "/missing", false, "/missing"); err != nil || f.Available() {
		t.Fatalf("optional tooling changed without sandbox: %v", err)
	}
}

func TestSandboxOutputRejectsSymlinksAndDirectories(t *testing.T) {
	root := t.TempDir()
	secret := filepath.Join(root, "secret")
	destination := filepath.Join(root, "destination")
	if err := os.WriteFile(secret, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "result")
	if err := os.Symlink(secret, link); err != nil {
		t.Fatal(err)
	}
	for _, src := range []string{link, root, filepath.Join(root, "missing")} {
		if err := copySandboxOutput(src, destination); err == nil {
			t.Fatalf("unsafe output accepted: %s", src)
		}
	}
	if f, err := openSandboxOutput(link); err == nil {
		testutil.Close(t, f)
		t.Fatal("output opener followed a symlink")
	}
	if data, err := os.ReadFile(destination); err != nil || string(data) != "unchanged" {
		t.Fatalf("invalid output modified destination: %s %v", data, err)
	}
}

func TestRealHostileMediaToolCannotReachHostFilesOrNetwork(t *testing.T) {
	if os.Getenv("COMFYWARE_SANDBOX_TEST") != "1" {
		t.Skip("set COMFYWARE_SANDBOX_TEST=1 to require real Linux namespace tests")
	}
	t.Setenv("IMVAULT_SMTP_PASSWORD", "server-secret")
	root := t.TempDir()
	secret := filepath.Join(root, "private")
	input := filepath.Join(root, "input.mp4")
	for _, file := range []string{secret, input} {
		if err := os.WriteFile(file, []byte("private"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer host.Close()
	tool := filepath.Join(root, "hostile-tool")
	script := "#!/bin/sh\nif [ \"$1\" = -version ]; then exit 0; fi\n" +
		"test ! -e '" + secret + "' || exit 10\n" +
		"test -z \"$IMVAULT_SMTP_PASSWORD\" || exit 11\n" +
		"test -f /input/media.mp4 || exit 12\n" +
		"if curl --max-time 1 -fsS '" + host.URL + "' >/dev/null 2>&1; then exit 13; fi\n" +
		"if bwrap --unshare-user --ro-bind / / /usr/bin/true >/dev/null 2>&1; then exit 14; fi\n" +
		"printf '%s\\n' '{\"streams\":[{\"width\":32,\"height\":16}],\"format\":{\"duration\":\"0.2\"}}'\n"
	if err := os.WriteFile(tool, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	f, err := NewSandboxedFFmpeg(t.Context(), tool, tool, "bwrap")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Probe(t.Context(), input); err != nil {
		t.Fatal(err)
	}
}

func TestMediaArgumentMappingPreservesOptions(t *testing.T) {
	input, err := filepath.Abs("-i")
	if err != nil {
		t.Fatal(err)
	}
	args := mapMediaArguments([]string{"-i", "-i", "-f", "image2pipe", "pipe:1"}, input, "", "/input/media", "")
	if strings.Join(args, " ") != "-i /input/media -f image2pipe pipe:1" {
		t.Fatalf("filename changed a tool option: %v", args)
	}
}

func FuzzMediaArgumentMapping(f *testing.F) {
	for _, name := range []string{"-i", "clip with spaces.mp4", "日本語.webm", "quote'and;$HOME.mp4"} {
		f.Add(name)
	}
	f.Fuzz(func(t *testing.T, name string) {
		if name == "" || strings.ContainsAny(name, "/\x00") || name == "." || name == ".." {
			return
		}
		input := filepath.Join("/tmp/input-property", name)
		args := []string{"-i", input, "-f", "image2pipe", "pipe:1"}
		mapped := mapMediaArguments(args, input, "", "/input/media", "")
		if mapped[0] != "-i" || mapped[1] != "/input/media" || strings.Join(mapped[2:], " ") != "-f image2pipe pipe:1" {
			t.Fatalf("filename changed option or output semantics: %q: %v", name, mapped)
		}
	})
}

func TestRealFailedMediaJobKeepsDestinationAndCleansScratch(t *testing.T) {
	if os.Getenv("COMFYWARE_SANDBOX_TEST") != "1" {
		t.Skip("set COMFYWARE_SANDBOX_TEST=1 to require real media failure tests")
	}
	root := t.TempDir()
	t.Setenv("TMPDIR", root)
	input, output, tool := filepath.Join(root, "input.mp4"), filepath.Join(root, "output.mp4"), filepath.Join(root, "bad-tool")
	if err := os.WriteFile(input, []byte("input"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tool, []byte("#!/bin/sh\nif [ \"$1\" = -version ]; then exit 0; fi\nprintf broken > /output/result.mp4\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	f, err := NewSandboxedFFmpeg(t.Context(), tool, tool, "bwrap")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Scrub(t.Context(), input, output); err == nil {
		t.Fatal("failed tool reported success")
	}
	if data, err := os.ReadFile(output); err != nil || string(data) != "unchanged" {
		t.Fatalf("failed job replaced destination: %s %v", data, err)
	}
	if entries, err := filepath.Glob(filepath.Join(root, "imvault-media-*")); err != nil || len(entries) != 0 {
		t.Fatalf("job scratch survived failure: %v %v", entries, err)
	}
}

func TestSandboxJobHasNoInheritedCredentialsOrBroadMount(t *testing.T) {
	input := filepath.Join(t.TempDir(), "clip with & spaces.mp4")
	if err := os.WriteFile(input, nil, 0600); err != nil {
		t.Fatal(err)
	}
	f := &FFmpeg{bwrapPath: "/usr/bin/false"}
	cmd, finish, err := f.command(t.Context(), "/usr/bin/true", input, "", []string{"-i", input})
	if err != nil {
		t.Fatal(err)
	}
	defer abandon(finish)
	args := strings.Join(cmd.Args, "\n")
	for _, expected := range []string{"--unshare-net", "--disable-userns", "--die-with-parent", "--ro-bind\n" + input + "\n/input/media.mp4", "-i\n/input/media.mp4"} {
		if !strings.Contains(args, expected) {
			t.Errorf("missing %s", expected)
		}
	}
	if strings.Contains(args, "--bind\n"+filepath.Dir(input)+"\n") {
		t.Fatal("entire source directory mounted")
	}
	for _, entry := range cmd.Env {
		if strings.HasPrefix(entry, "IMVAULT_") || strings.HasPrefix(entry, "AWS_") || strings.HasPrefix(entry, "LD_") {
			t.Fatal("credential or loader environment leaked")
		}
	}
}

func TestRealSandboxedVideoLifecycle(t *testing.T) {
	if os.Getenv("COMFYWARE_SANDBOX_TEST") != "1" {
		t.Skip("set COMFYWARE_SANDBOX_TEST=1 to require real Linux namespace tests")
	}
	f, err := NewSandboxedFFmpeg(t.Context(), "ffmpeg", "ffprobe", "bwrap")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	clip := filepath.Join(root, "clip with spaces.mp4")
	cmd := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "color=c=blue:s=32x16:d=0.2", "-c:v", "mpeg4", "-threads", "1", "-metadata", "title=private title", "-y", clip)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("test clip: %s %v", out, err)
	}
	info, err := f.Probe(t.Context(), clip)
	if err != nil || info.Width != 32 || info.Height != 16 {
		t.Fatalf("sandboxed probe: %+v %v", info, err)
	}
	if data, err := f.Poster(t.Context(), clip, 0, 32); err != nil || len(data) == 0 {
		t.Fatalf("sandboxed poster: %d bytes %v", len(data), err)
	}
	clean := filepath.Join(root, "clean.mp4")
	if err := f.Scrub(t.Context(), clip, clean); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Probe(t.Context(), clean); err != nil {
		t.Fatal(err)
	}
	cmd = exec.Command("ffprobe", "-v", "error", "-show_entries", "format_tags=title", "-of", "json", clean)
	if out, err := cmd.CombinedOutput(); err != nil || strings.Contains(string(out), "private title") {
		t.Fatalf("sandboxed scrub retained private metadata: %s %v", out, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	if _, err := f.Probe(ctx, clip); err == nil || time.Since(start) > time.Second {
		t.Fatal("canceled sandbox processing did not stop promptly")
	}
}
