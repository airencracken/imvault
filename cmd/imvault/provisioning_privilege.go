package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"strings"
	"syscall"
)

// reexecProvisioningAsService makes `sudo imvault create-admin` use the same
// filesystem identity as the installed service. The child receives the
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
		return true, 1, errors.New("the configured Imvault service user is root; create-admin refuses to write its database as root")
	}
	dataDir, err := resolveProvisioningDataDir(paths)
	if err != nil {
		return true, 1, err
	}
	dbPath, dbPathSet, err := resolveProvisioningDBPath(paths)
	if err != nil {
		return true, 1, err
	}
	return reexecAsServiceUser(args, username, groupName, dataDir, dbPath, dbPathSet)
}

func reexecAsServiceUser(args []string, username, groupName, dataDir, dbPath string, dbPathSet bool) (bool, int, error) {
	account, err := user.Lookup(username)
	if err != nil {
		return true, 1, fmt.Errorf("look up Imvault service user %q: %w", username, err)
	}
	credential, err := serviceCredential(account, groupName)
	if err != nil {
		return true, 1, err
	}
	executable, err := os.Executable()
	if err != nil {
		return true, 1, fmt.Errorf("find Imvault executable: %w", err)
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
		return true, 1, fmt.Errorf("run create-admin as service user %q: %w", username, err)
	}
	return true, 0, nil
}

func shouldReexecProvisioning(args []string) bool {
	return os.Geteuid() == 0 && len(args) > 0 && args[0] == "create-admin" && !hasHelpFlag(args[1:])
}

func serviceCredential(account *user.User, groupName string) (*syscall.Credential, error) {
	uid, err := strconv.ParseUint(account.Uid, 10, 32)
	if err != nil || uid == 0 {
		return nil, fmt.Errorf("Imvault service user %q must have a non-root numeric UID", account.Username)
	}
	gid, err := serviceGroupID(groupName, account.Gid)
	if err != nil {
		return nil, err
	}
	if gid == 0 {
		return nil, fmt.Errorf("Imvault service group %q must have a non-root numeric GID", groupName)
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
		return nil, fmt.Errorf("look up groups for Imvault service user %q: %w", account.Username, err)
	}
	groups := make([]uint32, 0, len(groupIDs))
	for _, id := range groupIDs {
		parsed, err := strconv.ParseUint(id, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("invalid supplementary group ID %q for Imvault service user %q", id, account.Username)
		}
		if parsed == 0 {
			return nil, fmt.Errorf("Imvault service user %q belongs to the root group", account.Username)
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
