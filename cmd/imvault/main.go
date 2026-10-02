// SPDX-License-Identifier: AGPL-3.0-or-later

// Command imvault runs the image host.
//
// Configuration is entirely environment-driven; see the README for the
// available IMVAULT_* variables.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/airencracken/comfylib/sandbox"
	"github.com/airencracken/comfylib/smtp"

	"imvault/internal/config"
	"imvault/internal/db"
	"imvault/internal/instance"
	"imvault/internal/logging"
	"imvault/internal/media"
	"imvault/internal/secrets"
	"imvault/internal/storage"
	"imvault/internal/store"
	"imvault/internal/web"
)

// shutdownGrace is how long requests in flight get to finish after SIGTERM.
// The service definitions in contrib wait longer than this before killing.
const shutdownGrace = 15 * time.Second

// version is set at build time with -ldflags "-X main.version=...", which the
// Makefile and the release configuration both do.
var version = ""

// buildVersion reports what this binary is: the stamped version, else the
// module version `go install` records, else "devel".
func buildVersion() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "devel"
}

func main() {
	if handled, status, err := reexecProvisioningAsService(os.Args[1:]); handled {
		if err != nil {
			slog.Error("Imvault", "error", err)
		}
		os.Exit(status)
	}
	if err := runCommand(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		// Goes through slog so that every line this process emits, including
		// its last, is a logfmt record.
		slog.Error("Imvault", "error", err)
		os.Exit(1)
	}
}

// closeInto closes c when the function returns and keeps its error alongside
// any other. A database or lock that fails to close may not have flushed, and
// the operator should hear about it rather than see a clean exit.
func closeInto(err *error, c io.Closer) {
	*err = errors.Join(*err, c.Close())
}

func run() (err error) {
	// Built first, so a configuration failure is reported the same way as
	// everything else.
	logger := logging.New(os.Stdout, logging.LevelFromEnv())
	slog.SetDefault(logger)

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	if err := cfg.EnsureDirs(); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// Inside the service sandbox this process is PID 1 and inherits every
	// orphan in the namespace; elsewhere this does nothing.
	sandbox.StartReaper(ctx)
	lock, err := instance.AcquireServer(cfg.DBPath)
	if err != nil {
		return err
	}
	defer closeInto(&err, lock)

	database, err := db.Open(ctx, cfg.DBPath)
	if err != nil {
		return err
	}
	defer closeInto(&err, database)

	objects, err := storage.New(ctx, cfg.Storage)
	if err != nil {
		return err
	}

	video, err := media.ConfiguredVideo(ctx, cfg.FFmpegPath, cfg.FFprobePath, cfg.MediaSandbox, cfg.BwrapPath)
	if err != nil {
		return err
	}
	if !video.Available() {
		logger.Warn("ffmpeg not found; clips will still be accepted but get a placeholder poster and no duration check",
			"ffmpeg", cfg.FFmpegPath,
			"ffprobe", cfg.FFprobePath,
		)
	}

	processor := media.NewProcessor(
		cfg.ThumbMax,
		cfg.PreviewMax,
		cfg.JPEGQuality,
		cfg.MaxVideoDuration,
		video,
	)

	st := store.New(database)
	sender, err := mailSender(cfg, st, logger)
	if err != nil {
		return err
	}

	// The key beside the database encrypts the few values that have to be
	// readable again, which today means TOTP secrets.
	cipher, err := secrets.Load(cfg.SecretKeyFile, cfg.SecretKey)
	if err != nil {
		return err
	}

	srv, err := web.New(cfg, st, objects, processor, sender, cipher, logger)
	if err != nil {
		return err
	}

	httpServer := &http.Server{
		Addr:    cfg.Addr,
		Handler: srv.Handler(),
		// ReadHeaderTimeout guards against slowloris; there is deliberately no
		// WriteTimeout so large image downloads are not cut short.
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}

	srv.StartWorkers(ctx)

	serveErr := make(chan error, 1)
	go func() {
		logger.Info("Imvault listening",
			"version", buildVersion(),
			"addr", cfg.Addr,
			"data_dir", cfg.DataDir,
			"anonymous_uploads", cfg.AllowAnonymousUploads,
			"anonymous_ttl", cfg.AnonymousTTL.String(),
			"signup_open", cfg.AllowSignup,
		)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
	}()

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	logger.Info("stopped")
	return nil
}

// mailSender returns an SMTP sender when a relay is configured, and one that
// refuses every message otherwise. The refusing sender logs nothing: a reset
// link in a log is as good as the password it replaces.
//
// Password reset stays usable either way: without mail, administrators issue
// one-time links from the admin UI.
func mailSender(cfg *config.Config, st *store.Store, logger *slog.Logger) (smtp.Sender, error) {
	if cfg.SMTPHost == "" {
		logger.Info("mail is not configured; password resets use administrator-issued links")
		return smtp.Disabled{}, nil
	}

	relay, err := smtp.New(smtp.Config{
		Host:     cfg.SMTPHost,
		Port:     cfg.SMTPPort,
		Username: cfg.SMTPUsername,
		Password: cfg.SMTPPassword,
		From:     cfg.SMTPFrom,
		Mode:     cfg.SMTPTLS,
	})
	if err != nil {
		return nil, err
	}
	logger.Info("mail configured",
		"host", cfg.SMTPHost,
		"port", cfg.SMTPPort,
		"tls", string(cfg.SMTPTLS),
		"from", cfg.SMTPFrom,
	)

	// Queue in front of the relay so an outage delays mail rather than dropping
	// it, and so a failed message shows up in the admin UI.
	logger.Info("outbound mail is queued",
		"max_attempts", cfg.MailMaxAttempts,
		"retry_interval", cfg.MailRetryInterval.String(),
	)
	return web.NewMailQueue(st, relay, cfg.MailMaxAttempts, cfg.MailRetryInterval, logger), nil
}
