package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// reexecProvisioningAsService makes `sudo imvault create-admin`, and the
// maintenance commands, use the same filesystem identity as the installed
// service. Run as root, they would leave root-owned database journals and
// objects the service can no longer read. The child receives the
// already-resolved data directory so it does not need to read root-only config.
func reexecProvisioningAsService(args []string) (bool, int, error) {
	if !shouldReexecProvisioning(args) {
		return false, 0, nil
	}
	paths := defaultProvisioningConfigPaths()
	username, groupName, managed, err := resolveProvisioningServiceAccount(paths)
	if err != nil {
		return true, 1, err
	}
	if !managed {
		return false, 0, nil
	}
	if username == "root" {
		return true, 1, fmt.Errorf("the configured service user is root; %s refuses to write the instance's files as root", args[0])
	}
	dataDir, err := resolveProvisioningDataDir(paths)
	if err != nil {
		return true, 1, err
	}
	dbPath, _, err := resolveProvisioningDBPath(paths)
	if err != nil {
		return true, 1, err
	}
	dbPath = provisioningDBPathForChild(dataDir, dbPath)
	return reexecAsServiceUser(args, username, groupName, dataDir, dbPath, true)
}

func provisioningDBPathForChild(dataDir, dbPath string) string {
	if dbPath != "" {
		return dbPath
	}
	return filepath.Join(dataDir, "imvault.db")
}

func reexecAsServiceUser(args []string, username, groupName, dataDir, dbPath string, dbPathSet bool) (bool, int, error) {
	account, err := user.Lookup(username)
	if err != nil {
		return true, 1, fmt.Errorf("look up service user %q: %w", username, err)
	}
	credential, err := serviceCredential(account, groupName)
	if err != nil {
		return true, 1, err
	}
	executable, err := os.Executable()
	if err != nil {
		return true, 1, fmt.Errorf("find the imvault executable: %w", err)
	}
	command := exec.Command(executable, args...)
	command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
	command.Env = withDataDirEnvironment(os.Environ(), "IMVAULT_DATA_DIR", dataDir)
	if dbPathSet {
		command.Env = withDataDirEnvironment(command.Env, "IMVAULT_DB", dbPath)
	}
	command.SysProcAttr = &syscall.SysProcAttr{Credential: credential}
	if err := command.Run(); err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			return true, exitError.ExitCode(), nil
		}
		return true, 1, fmt.Errorf("run %s as service user %q: %w", args[0], username, err)
	}
	return true, 0, nil
}

// geteuid is replaced in tests.
var geteuid = os.Geteuid

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

func shouldReexecProvisioning(args []string) bool {
	return geteuid() == 0 && len(args) > 0 && serviceUserCommands[args[0]] && !hasHelpFlag(args[1:])
}

// refuseRootMaintenance stops a maintenance command run as root when no
// installed service says which account it should run as instead.
func refuseRootMaintenance(command string) error {
	if geteuid() != 0 {
		return nil
	}
	return fmt.Errorf("%s writes the instance's files and must not run as root; run it as the service account, for example: sudo -u imvault env IMVAULT_DATA_DIR=/var/lib/imvault imvault %s", command, command)
}

func serviceCredential(account *user.User, groupName string) (*syscall.Credential, error) {
	uid, err := strconv.ParseUint(account.Uid, 10, 32)
	if err != nil || uid == 0 {
		return nil, fmt.Errorf("the service user %q must have a non-root numeric UID", account.Username)
	}
	gid, err := serviceGroupID(groupName, account.Gid)
	if err != nil {
		return nil, err
	}
	if gid == 0 {
		return nil, fmt.Errorf("the service group %q must have a non-root numeric GID", groupName)
	}
	groups, err := serviceSupplementaryGroups(account)
	if err != nil {
		return nil, err
	}
	return &syscall.Credential{Uid: uint32(uid), Gid: gid, Groups: groups}, nil
}

func serviceSupplementaryGroups(account *user.User) ([]uint32, error) {
	groupIDs, err := account.GroupIds()
	if err != nil {
		return nil, fmt.Errorf("look up groups for service user %q: %w", account.Username, err)
	}
	groups := make([]uint32, 0, len(groupIDs))
	for _, id := range groupIDs {
		parsed, err := strconv.ParseUint(id, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("invalid supplementary group ID %q for service user %q", id, account.Username)
		}
		if parsed == 0 {
			return nil, fmt.Errorf("the service user %q belongs to the root group", account.Username)
		}
		groups = append(groups, uint32(parsed))
	}
	return groups, nil
}

func hasHelpFlag(args []string) bool {
	for _, arg := range args {
		if arg == "--help" || arg == "-h" {
			return true
		}
	}
	return false
}

func serviceGroupID(groupName, fallbackGID string) (uint32, error) {
	if groupName == "" {
		groupName = fallbackGID
	}
	group, err := user.LookupGroup(groupName)
	if err == nil {
		groupID, parseErr := strconv.ParseUint(group.Gid, 10, 32)
		if parseErr != nil {
			return 0, fmt.Errorf("invalid group ID %q for service group %q", group.Gid, groupName)
		}
		return uint32(groupID), nil
	}
	if groupID, parseErr := strconv.ParseUint(groupName, 10, 32); parseErr == nil {
		return uint32(groupID), nil
	}
	return 0, fmt.Errorf("look up service group %q: %w", groupName, err)
}

func withDataDirEnvironment(environment []string, name, value string) []string {
	prefix := name + "="
	filtered := make([]string, 0, len(environment)+1)
	for _, item := range environment {
		if !strings.HasPrefix(item, prefix) {
			filtered = append(filtered, item)
		}
	}
	return append(filtered, prefix+value)
}
