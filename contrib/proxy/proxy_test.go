//go:build proxyintegration

package proxy_test

import (
	"bufio"
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"imvault/contrib"
)

func TestReverseProxies(t *testing.T) {
	for _, kind := range []string{"nginx", "apache"} {
		t.Run(kind, func(t *testing.T) {
			stream := make(chan struct{})
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/range":
					http.ServeContent(w, r, "clip.mp4", time.Time{}, strings.NewReader("0123456789"))
				case "/stream":
					fmt.Fprintln(w, "first")
					w.(http.Flusher).Flush()
					select {
					case <-stream:
					case <-r.Context().Done():
					}
				case "/private":
					http.Error(w, "permission denied", http.StatusForbidden)
				default:
					body, err := io.ReadAll(r.Body)
					if err != nil {
						http.Error(w, err.Error(), http.StatusBadRequest)
						return
					}
					http.SetCookie(w, &http.Cookie{Name: "session", Value: "test", Secure: true, HttpOnly: true, Path: "/"})
					json.NewEncoder(w).Encode(struct {
						Host, URI, Method, Digest string
						Headers                   http.Header
					}{r.Host, r.RequestURI, r.Method, fmt.Sprintf("%x", sha256.Sum256(body)), r.Header})
				}
			}))
			defer backend.Close()
			defer close(stream)
			plain, secure := startProxy(t, kind, backend.URL)
			client := &http.Client{
				Timeout:       5 * time.Second,
				Transport:     &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}, // Local test certificate only.
				CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
			}
			defer client.CloseIdleConnections()
			t.Run("redirect", func(t *testing.T) {
				resp := request(t, client, "GET", plain+"/photo%20name?q=a%2Bb", nil)
				defer resp.Body.Close()
				if resp.StatusCode != 308 || resp.Header.Get("Location") != "https://localhost/photo%20name?q=a%2Bb" {
					t.Fatalf("redirect: status=%d location=%q", resp.StatusCode, resp.Header.Get("Location"))
				}
			})
			t.Run("headers-and-upload", func(t *testing.T) {
				// Exceeds nginx's default 1 MiB limit; also exercises chunked uploads.
				body := bytes.Repeat([]byte("upload data\n"), 200000)
				req, err := http.NewRequest("POST", secure+"/photo%20name?q=a%2Bb", struct{ io.Reader }{bytes.NewReader(body)})
				if err != nil {
					t.Fatal(err)
				}
				req.Header.Set("Content-Type", "application/octet-stream")
				req.Header.Set("X-Forwarded-For", "198.51.100.7, 203.0.113.9")
				req.Header.Add("X-Forwarded-For", "192.0.2.4")
				req.Header.Set("X-Real-IP", "192.0.2.4")
				req.Header.Set("X-Forwarded-Proto", "http")
				req.Header.Set("X-Forwarded-Host", "spoof.example")
				req.Header.Set("Forwarded", "for=192.0.2.4;proto=http")
				req.Header.Set("Authorization", "Bearer test-key")
				req.Header.Set("Cookie", "session=test")
				req.Header.Set("HX-Request", "true")
				req.Header.Set("Origin", secure)
				resp, err := client.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				if resp.StatusCode != 200 {
					t.Fatalf("upload status=%d", resp.StatusCode)
				}
				var got struct {
					Host, URI, Method, Digest string
					Headers                   http.Header
				}
				if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
					t.Fatal(err)
				}
				if got.Host != strings.TrimPrefix(secure, "https://") || got.URI != "/photo%20name?q=a%2Bb" || got.Method != "POST" || got.Digest != fmt.Sprintf("%x", sha256.Sum256(body)) {
					t.Fatalf("request changed in transit: %+v", got)
				}
				for key, want := range map[string]string{
					"X-Forwarded-For": "127.0.0.1", "X-Real-IP": "127.0.0.1",
					"X-Forwarded-Proto": "https", "X-Forwarded-Host": got.Host, "Forwarded": "",
					"Authorization": "Bearer test-key", "Cookie": "session=test", "HX-Request": "true", "Origin": secure,
				} {
					if value := got.Headers.Get(key); value != want {
						t.Errorf("%s=%q; want %q", key, value, want)
					}
				}
				cookies := resp.Cookies()
				if len(cookies) != 1 || !cookies[0].Secure || !cookies[0].HttpOnly {
					t.Fatalf("cookie changed: %v", cookies)
				}
			})
			t.Run("range-and-access", func(t *testing.T) {
				req, err := http.NewRequest("GET", secure+"/range", nil)
				if err != nil {
					t.Fatal(err)
				}
				req.Header.Set("Range", "bytes=2-5")
				resp, err := client.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				body, err := io.ReadAll(resp.Body)
				resp.Body.Close()
				if err != nil {
					t.Fatal(err)
				}
				if resp.StatusCode != 206 || string(body) != "2345" || resp.Header.Get("Content-Range") != "bytes 2-5/10" {
					t.Fatalf("range: %d %q %v", resp.StatusCode, body, resp.Header)
				}
				resp = request(t, client, "GET", secure+"/private", nil)
				defer resp.Body.Close()
				if resp.StatusCode != 403 {
					t.Fatalf("private status=%d", resp.StatusCode)
				}
			})
			t.Run("streaming", func(t *testing.T) {
				resp := request(t, client, "GET", secure+"/stream", nil)
				defer resp.Body.Close()
				line, err := bufio.NewReader(resp.Body).ReadString('\n')
				if err != nil || line != "first\n" {
					t.Fatalf("stream held until completion: %q %v", line, err)
				}
			})
		})
	}
}

func request(t *testing.T, client *http.Client, method, url string, body io.Reader) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func freePort(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return fmt.Sprint(listener.Addr().(*net.TCPAddr).Port)
}

func certificate(t *testing.T, dir string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), DNSNames: []string{"localhost"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "cert.pem"), string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})))
	private, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "key.pem"), string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private})))
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func startProxy(t *testing.T, kind, backend string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	certDir := filepath.Join(dir, "TLS certs")
	if err := os.Mkdir(certDir, 0o700); err != nil {
		t.Fatal(err)
	}
	certificate(t, certDir)
	plain, secure := freePort(t), freePort(t)
	data, err := proxyconfig.Render(proxyconfig.Options{
		Server: kind, Domain: "localhost", Upstream: strings.TrimPrefix(backend, "http://"),
		Certificate: filepath.Join(certDir, "cert.pem"), Key: filepath.Join(certDir, "key.pem"),
	})
	if err != nil {
		t.Fatal(err)
	}
	config := strings.NewReplacer(
		"listen [::]:80;", "", "listen [::]:443 ssl;", "",
		"listen 80;", "listen 127.0.0.1:"+plain+";", "listen 443 ssl;", "listen 127.0.0.1:"+secure+" ssl;",
		"*:80", "127.0.0.1:"+plain, "*:443", "127.0.0.1:"+secure,
	).Replace(data)
	binary := os.Getenv(strings.ToUpper(kind) + "_BINARY")
	if binary == "" {
		name := kind
		if kind == "apache" {
			name = "apache2"
		}
		binary, err = exec.LookPath(name)
		if err != nil {
			t.Fatalf("install %s or set %s_BINARY: %v", name, strings.ToUpper(kind), err)
		}
	}
	path := filepath.Join(dir, "proxy.conf")
	args := []string{"-f", path, "-DFOREGROUND"}
	check := []string{"-f", path, "-t"}
	if kind == "nginx" {
		config = fmt.Sprintf("daemon off;\nmaster_process off;\npid %s/pid;\nerror_log stderr;\nevents {}\nhttp {\naccess_log off;\nclient_body_temp_path %s/body;\nproxy_temp_path %s/proxy;\n%s\n}\n", dir, dir, dir, config)
		args = []string{"-p", dir, "-c", path}
		check = append(append([]string{}, args...), "-t")
	} else {
		config = apachePreamble(t, binary, dir, plain, secure) + config
	}
	write(t, path, config)
	if output, err := exec.Command(binary, check...).CombinedOutput(); err != nil {
		t.Fatalf("config validation: %v\n%s", err, output)
	}
	log, err := os.Create(filepath.Join(dir, "process.log"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, args...)
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		log.Close()
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			cmd.Process.Kill()
			<-done
		}
		log.Close()
		if t.Failed() {
			for _, name := range []string{"process.log", "error.log"} {
				if data, err := os.ReadFile(filepath.Join(dir, name)); err == nil {
					t.Logf("%s:\n%s", name, data)
				}
			}
		}
	})
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", "127.0.0.1:"+secure, 100*time.Millisecond)
		if err == nil {
			conn.Close()
			return "http://localhost:" + plain, "https://localhost:" + secure
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("proxy did not start")
	return "", ""
}

func apachePreamble(t *testing.T, binary, dir, plain, secure string) string {
	t.Helper()
	modules := os.Getenv("APACHE_MODULE_DIR")
	if modules == "" {
		modules = "/usr/lib/apache2/modules"
	}
	compiled, err := exec.Command(binary, "-l").CombinedOutput()
	if err != nil {
		t.Fatalf("apache modules: %v %s", err, compiled)
	}
	var config strings.Builder
	fmt.Fprintf(&config, "ServerRoot %q\nDefaultRuntimeDir %q\nPidFile %q\nErrorLog %q\nServerName localhost\nListen 127.0.0.1:%s\nListen 127.0.0.1:%s\n", dir, dir, filepath.Join(dir, "pid"), filepath.Join(dir, "error.log"), plain, secure)
	for _, module := range []string{"mpm_event", "unixd", "authz_core", "alias", "ssl", "socache_shmcb", "headers", "proxy", "proxy_http"} {
		if !strings.Contains(string(compiled), "mod_"+module+".c") && !strings.Contains(string(compiled), module+".c") {
			fmt.Fprintf(&config, "LoadModule %s_module %q\n", module, filepath.Join(modules, "mod_"+module+".so"))
		}
	}
	fmt.Fprintf(&config, "User #%d\nGroup #%d\n", os.Getuid(), os.Getgid())
	return config.String()
}
