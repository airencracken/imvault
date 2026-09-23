// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
)

const commandHelp = `imvault - a photo and clip home for your people

Usage: imvault [COMMAND] [OPTIONS]

Commands:
  serve                Start the HTTP server (also the default with no arguments).
  create-admin         Provision a new administrator using a password from stdin.
  refresh-metadata     Refresh photo details from the stored originals.
  migrate-storage      Copy and verify objects to IMVAULT_DEST_* storage.
  rebuild-thumbnails   Regenerate previews and video posters.
  backup               Create a verified backup with --output DIRECTORY.
  restore              Restore --input BACKUP into a new --output DIRECTORY.
  proxy-config         Print a Caddy, nginx, or Apache HTTPS site configuration.
  help [COMMAND]       Show help; COMMAND --help also works.

Server configuration uses environment variables:
  IMVAULT_ADDR           Listen address (default: :8080; use 127.0.0.1:8080 behind a proxy).
  IMVAULT_DATA_DIR       Database and local media directory (default: ./data).
  IMVAULT_BASE_URL       Public origin, for example https://img.example.com.
  IMVAULT_SECURE_COOKIES Set true when using HTTPS (default: false).
  IMVAULT_ALLOW_SIGNUP   Allow registration (default: true).
  IMVAULT_ALLOW_ANONYMOUS_UPLOADS  Allow anonymous uploads (default: true).
  IMVAULT_STORAGE        disk (default) or s3; see docs/storage.md for storage settings.

Native service settings: /etc/conf.d/imvault (OpenRC), /etc/imvault/imvault.env (systemd).
The CLI reads the environment; it does not source service configuration files.
Run account/maintenance commands with the service's data settings and user.
Stop the server before backup, restore, storage migration, or thumbnail rebuilding.

Examples:
  IMVAULT_ADDR=127.0.0.1:8080 IMVAULT_DATA_DIR=/var/lib/imvault imvault serve
  imvault create-admin --username alex --password-stdin
  imvault proxy-config caddy --domain img.example.com > imvault.Caddyfile
  imvault backup --output /var/backups/imvault/first

Logs: stdout/stderr; systemd uses journald, OpenRC uses /var/log/imvault.log.
OpenRC packages include /etc/logrotate.d/imvault; a cron job or timer runs rotation.
More settings and deployment examples: docs/deployment.md and docs/reverse-proxies.md.`

var commandDescriptions = map[string]string{
	"serve":              "Start the HTTP server using IMVAULT_* environment variables. See imvault --help for defaults.",
	"create-admin":       "Create a new administrator locally; existing accounts are never changed.\nRead one password line from stdin. Run as the service user with its IMVAULT_DATA_DIR.",
	"refresh-metadata":   "Refresh photo details from stored originals without changing sharing settings.\nUse the same IMVAULT_DATA_DIR and storage settings as the service.",
	"migrate-storage":    "Copy and verify objects to IMVAULT_DEST_* storage. Stop the server first.\nUse the service's data/storage settings and configure the destination; see docs/storage.md.",
	"rebuild-thumbnails": "Regenerate thumbnails, previews and video posters. Stop the server first.\nUse the service's IMVAULT_DATA_DIR and storage settings; video posters need ffmpeg.",
	"backup":             "Create a verified, self-contained backup in a new directory. Stop the server first.\nUse the service's IMVAULT_DATA_DIR and storage settings.",
	"restore":            "Verify a backup and restore it into a new local data directory.\nStop the server and restore its service configuration separately.",
}

func commandFlags(command string, out io.Writer) *flag.FlagSet {
	flags := flag.NewFlagSet("imvault "+command, flag.ContinueOnError)
	flags.SetOutput(out)
	flags.Usage = func() {
		fmt.Fprintf(out, "Usage: imvault %s [OPTIONS]\n\n%s\n\n", command, commandDescriptions[command])
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
