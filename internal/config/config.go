// SPDX-License-Identifier: AGPL-3.0-or-later

// Package config loads runtime configuration from the environment.
//
// Every knob has a working default so that `imvault` can be started with no
// configuration at all. Values are read from IMVAULT_* environment variables.
package config

import (
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"imvault/internal/models"
)

// Config holds all runtime settings for the server.
type Config struct {
	// Name is the default instance name when an administrator has not set one.
	Name string
	// Addr is the host:port the HTTP server listens on.
	Addr string
	// DataDir is the root directory for the database and uploaded objects.
	DataDir string
	// DBPath is the SQLite database file.
	DBPath  string
	Storage Storage
	// BaseURL is an optional absolute prefix used when generating links.
	BaseURL string
	// SourceURL is shown in the footer. AGPL section 13 asks that people
	// interacting with the program over a network be offered its source, so it
	// defaults to the upstream repository and can be blanked to hide it.
	SourceURL string

	// AllowSignup controls whether new accounts can register.
	AllowSignup bool
	// InviteOnly requires a valid invitation to register while signup is open.
	InviteOnly bool
	// AllowAnonymousUploads controls whether logged-out visitors may upload.
	AllowAnonymousUploads bool
	// AnonymousTTL is how long an anonymous upload survives before the reaper
	// deletes it.
	AnonymousTTL time.Duration
	// DefaultVisibility is the level a new upload gets when the uploader does
	// not choose one. It is the setting that most changes what an instance
	// feels like: private is a personal host, members is a group, and public
	// is a public one.
	DefaultVisibility models.Visibility
	// SessionTTL is the lifetime of a login session.
	SessionTTL time.Duration
	// CleanupInterval is how often the background reaper runs.
	CleanupInterval time.Duration

	// MaxUploadBytes caps the size of a single still image or animation.
	MaxUploadBytes int64
	// MaxVideoBytes caps the size of a single video clip.
	MaxVideoBytes int64
	// MaxVideoDuration caps how long a clip may run.
	MaxVideoDuration time.Duration
	// DefaultQuotaBytes is the storage cap applied to newly created accounts.
	// Zero means unlimited.
	DefaultQuotaBytes int64
	// MaxTotalBytes caps what the instance as a whole will store. Zero means
	// unlimited. It is the "this box has N bytes" ceiling, as opposed to the
	// per-account cap, and it is the one that matters on a small host.
	MaxTotalBytes int64
	// MaxConcurrentUploads bounds how many uploads are processed at once.
	// Rate limiting bounds the rate per identity, which does not bound
	// concurrency, and a video upload shells out to ffmpeg inside the request.
	MaxConcurrentUploads int
	// UploadRatePerHour and UploadBurst bound how many uploads one identity may
	// make. A rate of zero disables the limit.
	UploadRatePerHour float64
	UploadBurst       int
	// TrustProxyHeaders makes the rate limiter read the client address from
	// X-Forwarded-For / X-Real-IP. Only enable it when a reverse proxy you
	// control sets those headers, since they are otherwise client-supplied.
	TrustProxyHeaders bool
	// FFmpegPath and FFprobePath locate the tools used to probe clips and
	// extract poster frames. If they are missing, video support degrades to
	// placeholder posters instead of failing outright.
	FFmpegPath  string
	FFprobePath string
	// MediaSandbox requires confined FFmpeg and FFprobe; BwrapPath selects the launcher.
	MediaSandbox bool
	BwrapPath    string

	// SMTP configures outgoing mail. When the host is empty, mail is disabled
	// and password resets are issued by an administrator instead.
	SMTPHost     string
	SMTPPort     int
	SMTPUsername string
	SMTPPassword string
	SMTPFrom     string
	SMTPTLS      string

	// PasswordResetTTL and EmailVerifyTTL bound the lifetime of the one-time
	// tokens those flows issue.
	PasswordResetTTL time.Duration
	EmailVerifyTTL   time.Duration

	// TOTPIssuer is the name an authenticator app shows for the account.
	TOTPIssuer string

	// OIDC is an optional OpenID Connect provider. Everything here is off
	// unless an issuer and a client id are given, and a partial configuration
	// is refused rather than half-applied.
	OIDCIssuer         string
	OIDCClientID       string
	OIDCClientSecret   string
	OIDCName           string
	OIDCScopes         []string
	OIDCAllowedDomains []string
	// SecretKey encrypts data that has to be readable again, which today means
	// TOTP secrets. SecretKeyFile is where the key is kept when SecretKey is
	// not supplied directly.
	SecretKey     string
	SecretKeyFile string

	// LoginRatePerHour and LoginBurst bound sign-in attempts, which matters
	// more once a second factor can be brute forced.
	LoginRatePerHour float64
	LoginBurst       int

	// MailMaxAttempts and MailRetryInterval govern the outbound queue: how many
	// times a message is tried before it is parked as failed, and how often the
	// worker looks for due messages.
	MailMaxAttempts   int
	MailRetryInterval time.Duration
	// SecureCookies sets the Secure flag on cookies. Enable behind TLS.
	SecureCookies bool

	// ThumbMax is the bounding box (in pixels) for generated thumbnails.
	ThumbMax int
	// PreviewMax is the bounding box for generated previews.
	PreviewMax int
	// JPEGQuality is the encoder quality for lossy thumbnails/previews.
	JPEGQuality int

	// MapURL is an optional static-map URL template used to draw a photo's
	// location on its page. The feature is off unless it is set. Placeholders
	// {lat}, {lon}, {zoom}, and {key} are substituted server-side, so the key
	// never reaches the browser.
	MapURL  string
	MapKey  string
	MapZoom int
}

// Load reads configuration from the environment, applying defaults.
func Load() (*Config, error) {
	return load("", "")
}

// LoadWithProvisioningPaths reads the environment like Load, using the supplied
// paths only when their corresponding environment variables are unset. The
// create-admin command uses this to honor service-manager paths without
// mutating process environment shared with other commands.
func LoadWithProvisioningPaths(dataDir, dbPath string) (*Config, error) {
	return load(dataDir, dbPath)
}

func load(dataDirOverride, dbPathOverride string) (*Config, error) {
	env := &reader{}
	dataDir := getenv("IMVAULT_DATA_DIR", "./data")
	if os.Getenv("IMVAULT_DATA_DIR") == "" && dataDirOverride != "" {
		dataDir = dataDirOverride
	}

	c := &Config{
		Name:    getenv("IMVAULT_NAME", "Imvault"),
		Addr:    getenv("IMVAULT_ADDR", ":8080"),
		DataDir: dataDir,
		BaseURL: strings.TrimRight(getenv("IMVAULT_BASE_URL", ""), "/"),
		// Set but empty hides the link, so empty is a value here.
		SourceURL:             lookupenv("IMVAULT_SOURCE_URL", "https://github.com/airencracken/imvault"),
		AllowSignup:           env.boolean("IMVAULT_ALLOW_SIGNUP", true),
		InviteOnly:            env.boolean("IMVAULT_INVITE_ONLY", false),
		AllowAnonymousUploads: env.boolean("IMVAULT_ALLOW_ANONYMOUS_UPLOADS", true),
		AnonymousTTL:          env.duration("IMVAULT_ANONYMOUS_TTL", 24*time.Hour),
		DefaultVisibility:     models.Visibility(getenv("IMVAULT_DEFAULT_VISIBILITY", string(models.VisibilityMembers))),
		SessionTTL:            env.duration("IMVAULT_SESSION_TTL", 30*24*time.Hour),
		CleanupInterval:       env.duration("IMVAULT_CLEANUP_INTERVAL", 15*time.Minute),
		MaxUploadBytes:        env.int64("IMVAULT_MAX_UPLOAD_BYTES", 32<<20),
		MaxVideoBytes:         env.int64("IMVAULT_MAX_VIDEO_BYTES", 128<<20),
		MaxVideoDuration:      env.duration("IMVAULT_MAX_VIDEO_DURATION", 60*time.Second),
		DefaultQuotaBytes:     env.int64("IMVAULT_DEFAULT_QUOTA_BYTES", 5<<30),
		MaxTotalBytes:         env.int64("IMVAULT_MAX_TOTAL_BYTES", 0),
		MaxConcurrentUploads:  env.integer("IMVAULT_MAX_CONCURRENT_UPLOADS", 0),
		UploadRatePerHour:     env.float("IMVAULT_UPLOAD_RATE_PER_HOUR", 120),
		UploadBurst:           env.integer("IMVAULT_UPLOAD_BURST", 20),
		TrustProxyHeaders:     env.boolean("IMVAULT_TRUST_PROXY_HEADERS", false),
		// Set but empty turns clip tooling off.
		FFmpegPath:       lookupenv("IMVAULT_FFMPEG", "ffmpeg"),
		FFprobePath:      lookupenv("IMVAULT_FFPROBE", "ffprobe"),
		BwrapPath:        getenv("IMVAULT_BWRAP", "bwrap"),
		SMTPHost:         getenv("IMVAULT_SMTP_HOST", ""),
		SMTPPort:         env.integer("IMVAULT_SMTP_PORT", 587),
		SMTPUsername:     getenv("IMVAULT_SMTP_USERNAME", ""),
		SMTPPassword:     getenv("IMVAULT_SMTP_PASSWORD", ""),
		SMTPFrom:         getenv("IMVAULT_SMTP_FROM", "Imvault <no-reply@localhost>"),
		SMTPTLS:          getenv("IMVAULT_SMTP_TLS", "starttls"),
		PasswordResetTTL: env.duration("IMVAULT_PASSWORD_RESET_TTL", time.Hour),
		EmailVerifyTTL:   env.duration("IMVAULT_EMAIL_VERIFY_TTL", 24*time.Hour),
		TOTPIssuer:       getenv("IMVAULT_TOTP_ISSUER", "Imvault"),
		// Kept exactly: discovery compares it byte for byte with what the provider
		// says its issuer is, and some providers end theirs with a slash.
		OIDCIssuer:         getenv("IMVAULT_OIDC_ISSUER", ""),
		OIDCClientID:       getenv("IMVAULT_OIDC_CLIENT_ID", ""),
		OIDCClientSecret:   getenv("IMVAULT_OIDC_CLIENT_SECRET", ""),
		OIDCName:           getenv("IMVAULT_OIDC_NAME", ""),
		OIDCScopes:         getList("IMVAULT_OIDC_SCOPES"),
		OIDCAllowedDomains: getList("IMVAULT_OIDC_ALLOWED_DOMAINS"),
		SecretKey:          getenv("IMVAULT_SECRET_KEY", ""),
		LoginRatePerHour:   env.float("IMVAULT_LOGIN_RATE_PER_HOUR", 30),
		LoginBurst:         env.integer("IMVAULT_LOGIN_BURST", 10),
		MailMaxAttempts:    env.integer("IMVAULT_MAIL_MAX_ATTEMPTS", 5),
		MailRetryInterval:  env.duration("IMVAULT_MAIL_RETRY_INTERVAL", time.Minute),
		SecureCookies:      env.boolean("IMVAULT_SECURE_COOKIES", false),
		ThumbMax:           env.integer("IMVAULT_THUMB_MAX", 480),
		PreviewMax:         env.integer("IMVAULT_PREVIEW_MAX", 1600),
		JPEGQuality:        env.integer("IMVAULT_JPEG_QUALITY", 82),
		MapURL:             getenv("IMVAULT_MAP_URL", ""),
		MapKey:             getenv("IMVAULT_MAP_KEY", ""),
		MapZoom:            env.integer("IMVAULT_MAP_ZOOM", 13),
	}

	switch raw := getenv("IMVAULT_MEDIA_SANDBOX", "false"); raw {
	case "true":
		c.MediaSandbox = true
	case "false":
	default:
		env.fail("IMVAULT_MEDIA_SANDBOX", raw, "true or false")
	}
	if err := env.err(); err != nil {
		return nil, err
	}
	var err error
	c.DBPath = getenv("IMVAULT_DB", filepath.Join(dataDir, "imvault.db"))
	if os.Getenv("IMVAULT_DB") == "" && dbPathOverride != "" {
		c.DBPath = dbPathOverride
	}
	c.SecretKeyFile = getenv("IMVAULT_SECRET_KEY_FILE", filepath.Join(dataDir, "secret.key"))
	c.Storage, err = LoadStorage("IMVAULT_", filepath.Join(dataDir, "objects"))
	if err != nil {
		return nil, err
	}

	// Normalised before validation so that "Public" is accepted, and a typo is
	// reported rather than silently becoming the closed level.
	c.DefaultVisibility = models.Visibility(strings.ToLower(strings.TrimSpace(string(c.DefaultVisibility))))
	if !c.DefaultVisibility.Valid() {
		return nil, fmt.Errorf("IMVAULT_DEFAULT_VISIBILITY must be public, members or private, got %q",
			c.DefaultVisibility)
	}
	if c.MaxConcurrentUploads == 0 {
		c.MaxConcurrentUploads = max(2, runtime.NumCPU())
	}
	if err := c.validateMedia(); err != nil {
		return nil, err
	}
	if err := c.validateAccounts(); err != nil {
		return nil, err
	}
	if err := c.validateMap(); err != nil {
		return nil, err
	}
	return c, nil
}

// validateMap checks the optional location map. It is off when no template is
// given, and a partial or malformed configuration is refused rather than
// failing on the first photo somebody opens.
func (c *Config) validateMap() error {
	if c.MapURL == "" {
		return nil
	}
	parsed, err := url.Parse(c.MapURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
		return fmt.Errorf("IMVAULT_MAP_URL must be an HTTP(S) URL template")
	}
	if !strings.Contains(c.MapURL, "{lat}") || !strings.Contains(c.MapURL, "{lon}") {
		return fmt.Errorf("IMVAULT_MAP_URL must contain {lat} and {lon} placeholders")
	}
	if strings.Contains(c.MapURL, "{key}") && c.MapKey == "" {
		return fmt.Errorf("IMVAULT_MAP_KEY is required because IMVAULT_MAP_URL contains {key}")
	}
	if c.MapZoom < 1 || c.MapZoom > 20 {
		return fmt.Errorf("IMVAULT_MAP_ZOOM must be within 1..20, got %d", c.MapZoom)
	}
	return nil
}

func (c *Config) validateMedia() error {
	if c.MaxUploadBytes <= 0 {
		return fmt.Errorf("IMVAULT_MAX_UPLOAD_BYTES must be positive, got %d", c.MaxUploadBytes)
	}
	if c.MaxVideoBytes <= 0 {
		return fmt.Errorf("IMVAULT_MAX_VIDEO_BYTES must be positive, got %d", c.MaxVideoBytes)
	}
	if c.MaxVideoDuration <= 0 {
		return fmt.Errorf("IMVAULT_MAX_VIDEO_DURATION must be positive, got %s", c.MaxVideoDuration)
	}
	if c.DefaultQuotaBytes < 0 {
		return fmt.Errorf("IMVAULT_DEFAULT_QUOTA_BYTES must not be negative, got %d", c.DefaultQuotaBytes)
	}
	if c.MaxTotalBytes < 0 {
		return fmt.Errorf("IMVAULT_MAX_TOTAL_BYTES must not be negative, got %d", c.MaxTotalBytes)
	}
	if c.MaxConcurrentUploads < 0 {
		return fmt.Errorf("IMVAULT_MAX_CONCURRENT_UPLOADS must not be negative, got %d", c.MaxConcurrentUploads)
	}
	if c.SessionTTL <= 0 {
		return fmt.Errorf("IMVAULT_SESSION_TTL must be positive, got %s", c.SessionTTL)
	}
	if c.CleanupInterval <= 0 {
		return fmt.Errorf("IMVAULT_CLEANUP_INTERVAL must be positive, got %s", c.CleanupInterval)
	}
	if c.AnonymousTTL <= 0 {
		return fmt.Errorf("IMVAULT_ANONYMOUS_TTL must be positive, got %s", c.AnonymousTTL)
	}
	if c.ThumbMax <= 0 || c.PreviewMax <= 0 {
		return fmt.Errorf("thumbnail/preview bounds must be positive")
	}
	if c.JPEGQuality < 1 || c.JPEGQuality > 100 {
		return fmt.Errorf("IMVAULT_JPEG_QUALITY must be within 1..100, got %d", c.JPEGQuality)
	}
	return nil
}

func (c *Config) validateAccounts() error {
	if c.PasswordResetTTL <= 0 {
		return fmt.Errorf("IMVAULT_PASSWORD_RESET_TTL must be positive")
	}
	if c.EmailVerifyTTL <= 0 {
		return fmt.Errorf("IMVAULT_EMAIL_VERIFY_TTL must be positive")
	}
	if c.MailMaxAttempts <= 0 {
		return fmt.Errorf("IMVAULT_MAIL_MAX_ATTEMPTS must be positive")
	}
	if c.MailRetryInterval <= 0 {
		return fmt.Errorf("IMVAULT_MAIL_RETRY_INTERVAL must be positive")
	}
	switch c.SMTPTLS {
	case "starttls", "implicit", "none":
	default:
		return fmt.Errorf("IMVAULT_SMTP_TLS must be starttls, implicit or none, got %q", c.SMTPTLS)
	}
	if c.SMTPHost != "" {
		if c.SMTPPort < 1 || c.SMTPPort > 65535 {
			return fmt.Errorf("IMVAULT_SMTP_PORT must be within 1..65535, got %d", c.SMTPPort)
		}
		// Mailed links have to point somewhere. Building them from the
		// request would let a forged Host header send somebody's reset link
		// to an attacker's site.
		if c.BaseURL == "" {
			return fmt.Errorf("IMVAULT_BASE_URL is required when IMVAULT_SMTP_HOST is set")
		}
	}

	// A partly configured provider would be worse than none: the button would
	// appear and then fail at the provider.
	if (c.OIDCIssuer == "") != (c.OIDCClientID == "") {
		return fmt.Errorf("IMVAULT_OIDC_ISSUER and IMVAULT_OIDC_CLIENT_ID must be set together")
	}
	if c.OIDCIssuer != "" && c.BaseURL == "" {
		// The redirect URI is registered with the provider and must match
		// exactly. Deriving it from the request would let a forged Host header
		// choose it, and would break the moment a proxy changes the name.
		return fmt.Errorf("IMVAULT_BASE_URL is required when an OpenID Connect provider is configured")
	}
	return nil
}

// EnsureDirs creates the directories the server needs to run.
func (c *Config) EnsureDirs() error {
	dirs := []string{c.DataDir}
	if c.Storage.Driver == "disk" || c.Storage.Driver == "" {
		directory := c.Storage.Directory
		if directory == "" {
			directory = filepath.Join(c.DataDir, "objects")
		}
		dirs = append(dirs, directory)
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}
	return nil
}

// getList reads a comma or space separated list, trimming each entry and
// dropping the empties so a trailing comma is not a value.
func getList(key string) []string {
	raw, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(raw) == "" {
		return nil
	}

	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n'
	})
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		if value := strings.TrimSpace(field); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func getenv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

// lookupenv is getenv for the few settings where an empty value means
// something: present but blank is an answer, not an absence.
func lookupenv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return fallback
}

// reader parses typed settings, collecting every malformed one so a single
// start reports them all. A value that does not parse is never replaced by the
// default: "IMVAULT_ALLOW_SIGNUP=no" quietly meaning "yes" is exactly the kind
// of mistake a configuration error exists to catch.
//
// An empty value is still unset, as it is for every other setting.
type reader struct {
	errs []error
}

func (r *reader) raw(key string) (string, bool) {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
		return "", false
	}
	return raw, true
}

func (r *reader) fail(key, raw, want string) {
	r.errs = append(r.errs, fmt.Errorf("%s must be %s, got %q", key, want, raw))
}

func (r *reader) err() error { return errors.Join(r.errs...) }

func (r *reader) boolean(key string, fallback bool) bool {
	raw, ok := r.raw(key)
	if !ok {
		return fallback
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		r.fail(key, raw, "true or false")
		return fallback
	}
	return v
}

func (r *reader) int64(key string, fallback int64) int64 {
	raw, ok := r.raw(key)
	if !ok {
		return fallback
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		r.fail(key, raw, "a whole number")
		return fallback
	}
	return v
}

func (r *reader) integer(key string, fallback int) int {
	raw, ok := r.raw(key)
	if !ok {
		return fallback
	}
	v, err := strconv.ParseInt(raw, 10, 0)
	if err != nil {
		r.fail(key, raw, "a whole number")
		return fallback
	}
	return int(v)
}

func (r *reader) float(key string, fallback float64) float64 {
	raw, ok := r.raw(key)
	if !ok {
		return fallback
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		r.fail(key, raw, "a number")
		return fallback
	}
	return v
}

// duration accepts both Go durations ("24h") and bare seconds ("86400").
func (r *reader) duration(key string, fallback time.Duration) time.Duration {
	raw, ok := r.raw(key)
	if !ok {
		return fallback
	}
	if v, err := time.ParseDuration(raw); err == nil {
		return v
	}
	if secs, err := strconv.ParseInt(raw, 10, 64); err == nil && secs <= math.MaxInt64/int64(time.Second) && secs >= math.MinInt64/int64(time.Second) {
		return time.Duration(secs) * time.Second
	}
	r.fail(key, raw, `a duration such as "90s" or "24h", or a number of seconds`)
	return fallback
}
