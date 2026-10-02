// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"cmp"
	"os"
	"path/filepath"

	"github.com/airencracken/comfylib/privdrop"
	"github.com/airencracken/comfylib/svcconfig"
)

// serviceDataDir is where packages and the service definitions keep the
// instance when their configuration names no other directory.
const serviceDataDir = "/var/lib/imvault"

// servicePaths finds the installed OpenRC or systemd service, so a command run
// by hand acts on the instance the service runs.
func servicePaths() svcconfig.Paths {
	return svcconfig.Detect("imvault", serviceDataDir)
}

// serviceUserCommands are the commands that write the live instance's files and
// so must run as the service's account. restore is not among them: it writes a
// new directory, which the operator then hands to the service.
var serviceUserCommands = map[string]bool{
	"create-admin":       true,
	"backup":             true,
	"migrate-storage":    true,
	"rebuild-thumbnails": true,
	"refresh-metadata":   true,
}

// reexecProvisioningAsService makes `sudo imvault create-admin`, and the
// maintenance commands, use the same filesystem identity as the installed
// service. Run as root, they would leave root-owned database journals and
// objects the service can no longer read. The child receives the
// already-resolved data directory and database so it does not need to read
// root-only configuration.
func reexecProvisioningAsService(args []string) (bool, int, error) {
	paths := servicePaths()
	return privdrop.Reexec(privdrop.Request{
		Args:        args,
		Commands:    serviceUserCommands,
		Paths:       paths,
		DefaultUser: "imvault",
		Settings: func(_, dataDir string) (map[string]string, error) {
			dbPath, err := childDBPath(paths, dataDir)
			if err != nil {
				return nil, err
			}
			return map[string]string{"IMVAULT_DB": dbPath}, nil
		},
	})
}

// childDBPath resolves the database a re-run command should open: the
// environment's or the service's IMVAULT_DB, else the default inside the
// data directory already resolved for it.
func childDBPath(paths svcconfig.Paths, dataDir string) (string, error) {
	settings, err := paths.Settings("IMVAULT_DB")
	if err != nil {
		return "", err
	}
	return cmp.Or(settings["IMVAULT_DB"], filepath.Join(dataDir, "imvault.db")), nil
}

// geteuid is replaced in tests.
var geteuid = os.Geteuid

// refuseRootMaintenance stops a maintenance command run as root when no
// installed service says which account it should run as instead.
func refuseRootMaintenance(command string) error {
	return privdrop.RefuseRoot(geteuid(), command, "sudo -u imvault env IMVAULT_DATA_DIR="+serviceDataDir+" imvault "+command)
}
