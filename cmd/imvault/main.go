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
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"imvault/internal/config"
	"imvault/internal/db"
	"imvault/internal/logging"
	"imvault/internal/mail"
	"imvault/internal/media"
	"imvault/internal/secrets"
	"imvault/internal/storage"
	"imvault/internal/store"
	"imvault/internal/web"
)

func main() {
	if err := run(); err != nil {
		// Goes through slog so that every line this process emits, including
		// its last, is a logfmt record.
		slog.Error("imvault", "error", err)
		os.Exit(1)
	}
}

func run() error {
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

	database, err := db.Open(ctx, cfg.DBPath)
	if err != nil {
		return err
	}
	defer database.Close()

	objects, err := storage.NewDisk(filepath.Join(cfg.DataDir, "objects"))
	if err != nil {
		return err
	}

	video := media.NewFFmpeg(cfg.FFmpegPath, cfg.FFprobePath)
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
	sender := mailSender(cfg, st, logger)

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
		logger.Info("imvault listening",
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

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	logger.Info("stopped")
	return nil
}

// mailSender returns an SMTP sender when a relay is configured, and a sender
// that logs instead of delivering otherwise.
//
// Password reset stays usable either way: without mail, administrators issue
// one-time links from the admin UI.
func mailSender(cfg *config.Config, st *store.Store, logger *slog.Logger) mail.Sender {
	if cfg.SMTPHost == "" {
		logger.Info("mail is not configured; password resets use administrator-issued links")
		return mail.Disabled{Log: logger}
	}

	logger.Info("mail configured",
		"host", cfg.SMTPHost,
		"port", cfg.SMTPPort,
		"tls", cfg.SMTPTLS,
		"from", cfg.SMTPFrom,
	)

	relay := mail.NewSMTP(mail.Config{
		Host:     cfg.SMTPHost,
		Port:     cfg.SMTPPort,
		Username: cfg.SMTPUsername,
		Password: cfg.SMTPPassword,
		From:     cfg.SMTPFrom,
		Mode:     mail.TLSMode(cfg.SMTPTLS),
	})

	// Queue in front of the relay so an outage delays mail rather than dropping
	// it, and so a failed message shows up in the admin UI.
	logger.Info("outbound mail is queued",
		"max_attempts", cfg.MailMaxAttempts,
		"retry_interval", cfg.MailRetryInterval.String(),
	)
	return web.NewMailQueue(st, relay, cfg.MailMaxAttempts, cfg.MailRetryInterval, logger)
}
