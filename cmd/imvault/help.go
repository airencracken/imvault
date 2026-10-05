// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
)

const commandHelp = `Imvault - a photo and clip home for your people

Usage: imvault [COMMAND] [OPTIONS]

Commands:
  sandbox              Start the server confined by Bubblewrap (Linux).
  serve                Start the HTTP server (also the default with no arguments).
  create-admin         Provision a new administrator using a hidden prompt or stdin.
  refresh-metadata     Refresh photo details from the stored originals.
  migrate-storage      Copy and verify objects to IMVAULT_DEST_* storage.
  rebuild-thumbnails   Regenerate previews and video posters.
  backup               Create a verified snapshot with --output or --output-dir.
  restore              Restore --input BACKUP into a new --output DIRECTORY.
  proxy-config         Print a Caddy, nginx, or Apache HTTPS site configuration.
  help [COMMAND]       Show help; COMMAND --help also works.

Server configuration uses environment variables:
  IMVAULT_ADDR           Listen address (default: :8080; use 127.0.0.1:8080 behind a proxy).
  IMVAULT_DATA_DIR       Database and local media directory (default: ./data).
  IMVAULT_BASE_URL       Public origin, for example https://img.example.com.
  IMVAULT_SECURE_COOKIES Automatic with an HTTPS base URL; true requires it elsewhere.
  IMVAULT_ALLOW_SIGNUP   Allow registration (default: true).
  IMVAULT_INVITE_ONLY     Require invitations (default: true).
  IMVAULT_ALLOW_ANONYMOUS_UPLOADS  Allow anonymous uploads (default: false).
  IMVAULT_STORAGE        disk (default) or s3; see docs/storage.md for storage settings.

Native service settings: /etc/conf.d/imvault (OpenRC), /etc/imvault/imvault.env (systemd).
create-admin reads IMVAULT_DATA_DIR from the active service configuration when
it is not set in the environment. When run as root, it repeats the database
operation as the service user. Root-run backups also resolve storage and encryption settings from the service.
Other commands use the process environment; pass their service settings explicitly.
Backups can run alongside Imvault 0.13 or later; stop older servers first.
Stop the server before restore, storage migration, or thumbnail rebuilding.

Examples:
  IMVAULT_ADDR=127.0.0.1:8080 IMVAULT_DATA_DIR=/var/lib/imvault imvault serve
  imvault create-admin --username alex --password-prompt
  imvault create-admin --username alex --password-stdin < password-file
  imvault proxy-config caddy --domain img.example.com > imvault.Caddyfile
  imvault backup --output /var/backups/imvault/first

Logs: stdout/stderr; systemd uses journald, OpenRC uses /var/log/imvault.log.
OpenRC packages include /etc/logrotate.d/imvault; a cron job or timer runs rotation.
More settings and deployment examples: docs/deployment.md and docs/reverse-proxies.md.`

var commandDescriptions = map[string]string{
	"sandbox":            "Start the server confined by Bubblewrap. Run as the service user.\nThe data directory must already exist; see docs/sandbox.md.",
	"serve":              "Start the HTTP server using IMVAULT_* environment variables. See imvault --help for defaults.",
	"create-admin":       "Create a new administrator locally; existing accounts are never changed.\nUse a hidden terminal prompt or read one password line from stdin. IMVAULT_DATA_DIR is read from the active service configuration unless set in the environment. Root invocations use the configured service user.",
	"refresh-metadata":   "Refresh photo details from stored originals without changing sharing settings.\nUse the same IMVAULT_DATA_DIR and storage settings as the service.",
	"migrate-storage":    "Copy and verify objects to IMVAULT_DEST_* storage. Stop the server first.\nUse the service's data/storage settings and configure the destination; see docs/storage.md.",
	"rebuild-thumbnails": "Regenerate thumbnails, previews and video posters. Stop the server first.\nUse the service's IMVAULT_DATA_DIR and storage settings; video posters need ffmpeg.",
	"backup":             "Create a verified, self-contained backup in a new directory. Imvault 0.13 or later can stay running.\nChoose --output NEW_DIRECTORY or --output-dir EXISTING_PARENT for a dated snapshot.\nStop older servers first.\nUse the service's IMVAULT_DATA_DIR and storage settings, and the Imvault version that last ran the database; backup never migrates it.",
	"restore":            "Verify a backup and restore it into a new local data directory.\nStop the server and restore its service configuration separately.",
}

func commandFlags(command string, out io.Writer) *flag.FlagSet {
	flags := flag.NewFlagSet("imvault "+command, flag.ContinueOnError)
	flags.SetOutput(out)
	flags.Usage = func() {
		// Usage cannot return an error, and a terminal that cannot take the
		// help text cannot take an error message either.
		_, _ = fmt.Fprintf(out, "Usage: imvault %s [OPTIONS]\n\n%s\n\n", command, commandDescriptions[command])
		flags.PrintDefaults()
	}
	return flags
}

func serve(args []string, out io.Writer) error {
	flags := commandFlags("serve", out)
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected server arguments; use imvault serve --help")
	}
	return run()
}
