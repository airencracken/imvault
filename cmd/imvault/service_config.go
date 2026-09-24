package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type provisioningConfigPaths struct {
	openRCConfig    string
	openRCInstalled bool
	openRCActive    bool
	systemdUnit     string
	systemdActive   bool
	serviceDefault  string
}

type provisioningManager uint8

const (
	managerNone provisioningManager = iota
	managerOpenRC
	managerSystemd
	managerBoth
)

func defaultProvisioningConfigPaths() provisioningConfigPaths {
	openRCConfig := "/etc/conf.d/imvault"
	openRCInit := "/etc/init.d/imvault"
	_, openRCErr := os.Stat(openRCConfig)
	_, initErr := os.Stat(openRCInit)
	openRCInstalled := openRCErr == nil || initErr == nil
	if openRCErr != nil && !errors.Is(openRCErr, os.ErrNotExist) {
		openRCInstalled = true
	}
	if initErr != nil && !errors.Is(initErr, os.ErrNotExist) {
		openRCInstalled = true
	}
	var systemdUnit string
	for _, path := range []string{
		"/etc/systemd/system/imvault.service",
		"/run/systemd/system/imvault.service",
		"/usr/local/lib/systemd/system/imvault.service",
		"/usr/lib/systemd/system/imvault.service",
		"/lib/systemd/system/imvault.service",
	} {
		if _, err := os.Stat(path); err == nil {
			systemdUnit = path
			break
		}
	}
	_, openRCActiveErr := os.Stat("/run/openrc/softlevel")
	_, systemdActiveErr := os.Stat("/run/systemd/system")
	return provisioningConfigPaths{
		openRCConfig:    openRCConfig,
		openRCInstalled: openRCInstalled,
		openRCActive:    openRCActiveErr == nil,
		systemdUnit:     systemdUnit,
		systemdActive:   systemdActiveErr == nil,
		serviceDefault:  "/var/lib/imvault",
	}
}

func resolveProvisioningDataDir(paths provisioningConfigPaths) (string, error) {
	if value := os.Getenv("IMVAULT_DATA_DIR"); value != "" {
		return value, nil
	}
	openRCInstalled, systemdInstalled := installedProvisioningManagers(paths)
	switch selectProvisioningManager(paths, openRCInstalled, systemdInstalled) {
	case managerNone:
		return "./data", nil
	case managerOpenRC:
		return openRCProvisioningDataDir(paths)
	case managerSystemd:
		return systemdProvisioningDataDir(paths)
	case managerBoth:
		return matchingProvisioningDataDir(paths)
	}
	return "./data", nil
}

func resolveProvisioningDBPath(paths provisioningConfigPaths) (string, bool, error) {
	if value := os.Getenv("IMVAULT_DB"); value != "" {
		return value, true, nil
	}
	openRCInstalled, systemdInstalled := installedProvisioningManagers(paths)
	switch selectProvisioningManager(paths, openRCInstalled, systemdInstalled) {
	case managerNone:
		return "", false, nil
	case managerOpenRC:
		return readProvisioningSetting(paths, managerOpenRC, "IMVAULT_DB")
	case managerSystemd:
		return readProvisioningSetting(paths, managerSystemd, "IMVAULT_DB")
	case managerBoth:
		return matchingProvisioningDBPath(paths)
	}
	return "", false, nil
}

func resolveProvisioningServiceAccount(paths provisioningConfigPaths) (string, string, bool, error) {
	openRCInstalled, systemdInstalled := installedProvisioningManagers(paths)
	switch selectProvisioningManager(paths, openRCInstalled, systemdInstalled) {
	case managerNone:
		return "", "", false, nil
	case managerOpenRC:
		user, group, err := openRCServiceAccount(paths)
		return user, group, true, err
	case managerSystemd:
		user, group, err := systemdServiceAccount(paths)
		return user, group, true, err
	case managerBoth:
		return matchingProvisioningServiceAccount(paths)
	}
	return "", "", false, nil
}

func installedProvisioningManagers(paths provisioningConfigPaths) (openRC, systemd bool) {
	openRC = paths.openRCInstalled || paths.openRCConfig != "" && fileExists(paths.openRCConfig)
	systemd = paths.systemdUnit != "" && fileExists(paths.systemdUnit)
	return openRC, systemd
}

func selectProvisioningManager(paths provisioningConfigPaths, openRC, systemd bool) provisioningManager {
	if openRC && paths.openRCActive {
		return managerOpenRC
	}
	if systemd && paths.systemdActive {
		return managerSystemd
	}
	if openRC && systemd {
		return managerBoth
	}
	if openRC {
		return managerOpenRC
	}
	if systemd {
		return managerSystemd
	}
	return managerNone
}

func matchingProvisioningDataDir(paths provisioningConfigPaths) (string, error) {
	openRC, err := openRCProvisioningDataDir(paths)
	if err != nil {
		return "", err
	}
	systemd, err := systemdProvisioningDataDir(paths)
	if err != nil {
		return "", err
	}
	if filepath.Clean(openRC) != filepath.Clean(systemd) {
		return "", fmt.Errorf("OpenRC and systemd configure different Imvault data directories (%q and %q); set IMVAULT_DATA_DIR explicitly or run under the active service manager", openRC, systemd)
	}
	return openRC, nil
}

func matchingProvisioningDBPath(paths provisioningConfigPaths) (string, bool, error) {
	openRC, openRCSet, err := readProvisioningSetting(paths, managerOpenRC, "IMVAULT_DB")
	if err != nil {
		return "", false, err
	}
	systemd, systemdSet, err := readProvisioningSetting(paths, managerSystemd, "IMVAULT_DB")
	if err != nil {
		return "", false, err
	}
	if openRCSet != systemdSet || openRC != systemd {
		return "", false, fmt.Errorf("OpenRC and systemd configure different Imvault database paths; set IMVAULT_DB explicitly or run under the active service manager")
	}
	return openRC, openRCSet, nil
}

func readProvisioningSetting(paths provisioningConfigPaths, manager provisioningManager, key string) (string, bool, error) {
	if manager == managerOpenRC {
		return readShellConfigValue(paths.openRCConfig, key)
	}
	return readSystemdDataDir(paths.systemdUnit, key)
}

func matchingProvisioningServiceAccount(paths provisioningConfigPaths) (string, string, bool, error) {
	openRCUser, openRCGroup, err := openRCServiceAccount(paths)
	if err != nil {
		return "", "", true, err
	}
	systemdUser, systemdGroup, err := systemdServiceAccount(paths)
	if err != nil {
		return "", "", true, err
	}
	if openRCUser != systemdUser || openRCGroup != systemdGroup {
		return "", "", true, fmt.Errorf("OpenRC and systemd configure different Imvault service accounts (%s:%s and %s:%s); run under the active service manager", openRCUser, openRCGroup, systemdUser, systemdGroup)
	}
	return openRCUser, openRCGroup, true, nil
}

func openRCServiceAccount(paths provisioningConfigPaths) (string, string, error) {
	user, userSet, err := readShellConfigValue(paths.openRCConfig, "IMVAULT_USER")
	if err != nil {
		return "", "", err
	}
	group, groupSet, err := readShellConfigValue(paths.openRCConfig, "IMVAULT_GROUP")
	if err != nil {
		return "", "", err
	}
	if !userSet || user == "" {
		user = "imvault"
	}
	if !groupSet || group == "" {
		group = "imvault"
	}
	return user, group, nil
}

func systemdServiceAccount(paths provisioningConfigPaths) (string, string, error) {
	user, userSet, err := readSystemdServiceSetting(paths.systemdUnit, "User")
	if err != nil {
		return "", "", err
	}
	group, groupSet, err := readSystemdServiceSetting(paths.systemdUnit, "Group")
	if err != nil {
		return "", "", err
	}
	if !userSet || user == "" {
		user = "root"
	}
	if !groupSet {
		group = ""
	}
	return user, group, nil
}

func readSystemdServiceSetting(unitPath, key string) (string, bool, error) {
	if unitPath == "" {
		return "", false, nil
	}
	configPaths, err := systemdUnitConfigFiles(unitPath)
	if err != nil {
		return "", false, err
	}
	var value string
	found := false
	for _, path := range configPaths {
		fileValue, fileFound, err := readSystemdServiceFile(path, key)
		if err != nil {
			return "", false, err
		}
		if fileFound {
			value, found = fileValue, true
		}
	}
	return value, found, nil
}

func readSystemdServiceFile(path, key string) (string, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", false, fmt.Errorf("read systemd unit %s: %w", path, err)
	}
	defer file.Close()

	var value string
	found, inService := false, false
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		text := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(text, "[") && strings.HasSuffix(text, "]") {
			inService = text == "[Service]"
			continue
		}
		if !inService || text == "" || strings.HasPrefix(text, "#") || strings.HasPrefix(text, ";") {
			continue
		}
		name, raw, ok := strings.Cut(text, "=")
		if ok && strings.TrimSpace(name) == key {
			value, found = strings.Trim(strings.TrimSpace(raw), "\"'"), true
		}
	}
	if err := scanner.Err(); err != nil {
		return "", false, fmt.Errorf("read %s: %w", path, err)
	}
	return value, found, nil
}

func openRCProvisioningDataDir(paths provisioningConfigPaths) (string, error) {
	value, found, err := readShellConfigValue(paths.openRCConfig, "IMVAULT_DATA_DIR")
	if err != nil {
		return "", err
	}
	if !found || value == "" {
		value = paths.serviceDefault
	}
	return validateServiceDataDir(value, "OpenRC")
}

func systemdProvisioningDataDir(paths provisioningConfigPaths) (string, error) {
	value, found, err := readSystemdDataDir(paths.systemdUnit, "IMVAULT_DATA_DIR")
	if err != nil {
		return "", err
	}
	if !found || value == "" {
		value = paths.serviceDefault
	}
	return validateServiceDataDir(value, "systemd")
}

func validateServiceDataDir(value, manager string) (string, error) {
	if !filepath.IsAbs(value) {
		return "", fmt.Errorf("%s Imvault data directory %q is not absolute; set IMVAULT_DATA_DIR explicitly", manager, value)
	}
	return value, nil
}

func readShellConfigValue(path, key string) (string, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("read %s: %w; run create-owner as a user that can read the service configuration, or set IMVAULT_DATA_DIR explicitly", path, err)
	}
	defer file.Close()

	var value string
	found := false
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024), 64*1024)
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		if strings.HasPrefix(text, "export ") {
			text = strings.TrimSpace(strings.TrimPrefix(text, "export "))
		}
		name, raw, ok := strings.Cut(text, "=")
		if !ok || strings.TrimSpace(name) != key {
			continue
		}
		parsed, err := parseConfigValue(strings.TrimSpace(raw), true)
		if err != nil {
			return "", false, fmt.Errorf("parse %s:%d: %w; set IMVAULT_DATA_DIR explicitly if the value uses shell expansion", path, line, err)
		}
		value, found = parsed, true
	}
	if err := scanner.Err(); err != nil {
		return "", false, fmt.Errorf("read %s: %w", path, err)
	}
	return value, found, nil
}

func readSystemdDataDir(unitPath, key string) (string, bool, error) {
	if unitPath == "" {
		return "", false, nil
	}
	unitValues, environmentFiles, err := readSystemdUnitEnvironment(unitPath)
	if err != nil {
		return "", false, err
	}
	value, found := unitValues[key]
	return applySystemdEnvironmentFiles(unitPath, environmentFiles, key, value, found)
}

func readSystemdUnitEnvironment(unitPath string) (map[string]string, []string, error) {
	configPaths, err := systemdUnitConfigFiles(unitPath)
	if err != nil {
		return nil, nil, err
	}
	unitValues := make(map[string]string)
	var environmentFiles []string
	for _, configPath := range configPaths {
		if err := readSystemdUnitFile(configPath, unitValues, &environmentFiles); err != nil {
			return nil, nil, err
		}
	}
	return unitValues, environmentFiles, nil
}

func readSystemdUnitFile(path string, unitValues map[string]string, environmentFiles *[]string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("read systemd unit %s: %w", path, err)
	}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024), 64*1024)
	inService := false
	var parseErr error
	for line := 1; scanner.Scan(); line++ {
		inService, parseErr = applySystemdUnitLine(path, line, scanner.Text(), inService, unitValues, environmentFiles)
		if parseErr != nil {
			break
		}
	}
	if err := scanner.Err(); err != nil && parseErr == nil {
		parseErr = fmt.Errorf("read %s: %w", path, err)
	}
	if err := file.Close(); err != nil && parseErr == nil {
		parseErr = fmt.Errorf("close %s: %w", path, err)
	}
	return parseErr
}

func applySystemdUnitLine(path string, line int, rawLine string, inService bool, unitValues map[string]string, environmentFiles *[]string) (bool, error) {
	text := strings.TrimSpace(rawLine)
	if strings.HasPrefix(text, "[") && strings.HasSuffix(text, "]") {
		return text == "[Service]", nil
	}
	if !inService || text == "" || strings.HasPrefix(text, "#") || strings.HasPrefix(text, ";") {
		return inService, nil
	}
	name, raw, ok := strings.Cut(text, "=")
	if !ok {
		return inService, nil
	}
	switch strings.TrimSpace(name) {
	case "Environment":
		if err := applySystemdEnvironment(raw, unitValues); err != nil {
			return inService, fmt.Errorf("parse systemd unit %s:%d: %w", path, line, err)
		}
	case "EnvironmentFile":
		if err := applySystemdEnvironmentFilesDirective(raw, environmentFiles); err != nil {
			return inService, fmt.Errorf("parse systemd unit %s:%d: %w", path, line, err)
		}
	}
	return inService, nil
}

func applySystemdEnvironment(raw string, values map[string]string) error {
	if strings.TrimSpace(raw) == "" {
		clear(values)
		return nil
	}
	words, err := splitConfigWords(raw)
	if err != nil {
		return err
	}
	for _, word := range words {
		name, value, ok := strings.Cut(word, "=")
		if ok {
			values[name] = value
		}
	}
	return nil
}

func applySystemdEnvironmentFilesDirective(raw string, environmentFiles *[]string) error {
	if strings.TrimSpace(raw) == "" {
		*environmentFiles = nil
		return nil
	}
	words, err := splitConfigWords(raw)
	if err != nil {
		return err
	}
	*environmentFiles = append(*environmentFiles, words...)
	return nil
}

func applySystemdEnvironmentFiles(unitPath string, environmentFiles []string, key, value string, found bool) (string, bool, error) {
	for _, environmentFile := range environmentFiles {
		optional := strings.HasPrefix(environmentFile, "-")
		path := strings.TrimPrefix(environmentFile, "-")
		if !filepath.IsAbs(path) {
			return "", false, fmt.Errorf("systemd EnvironmentFile %q in %s is not an absolute path", path, unitPath)
		}
		fileValue, fileFound, err := readSystemdEnvironmentFile(path, key)
		if err != nil {
			if optional && errors.Is(err, os.ErrNotExist) {
				continue
			}
			return "", false, err
		}
		if fileFound {
			value, found = fileValue, true
		}
	}
	return value, found, nil
}

func systemdUnitConfigFiles(unitPath string) ([]string, error) {
	unitName := filepath.Base(unitPath)
	directories := []string{
		"/etc/systemd/system",
		"/run/systemd/system",
		"/usr/local/lib/systemd/system",
		"/usr/lib/systemd/system",
		"/lib/systemd/system",
	}
	selected := make(map[string]string)
	for _, directory := range directories {
		entries, err := os.ReadDir(filepath.Join(directory, unitName+".d"))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("read systemd overrides in %s: %w", filepath.Join(directory, unitName+".d"), err)
		}
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || !strings.HasSuffix(name, ".conf") {
				continue
			}
			if _, exists := selected[name]; !exists {
				selected[name] = filepath.Join(directory, unitName+".d", name)
			}
		}
	}
	names := make([]string, 0, len(selected))
	for name := range selected {
		names = append(names, name)
	}
	sort.Strings(names)
	files := []string{unitPath}
	for _, name := range names {
		files = append(files, selected[name])
	}
	return files, nil
}

func readSystemdEnvironmentFile(path, key string) (string, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", false, fmt.Errorf("read systemd environment file %s: %w", path, err)
	}
	defer file.Close()
	var value string
	found := false
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024), 64*1024)
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if text == "" || strings.HasPrefix(text, "#") || strings.HasPrefix(text, ";") {
			continue
		}
		name, raw, ok := strings.Cut(text, "=")
		if !ok || strings.TrimSpace(name) != key {
			continue
		}
		parsed, err := parseConfigValue(strings.TrimSpace(raw), false)
		if err != nil {
			return "", false, fmt.Errorf("parse %s:%d: %w", path, line, err)
		}
		value, found = parsed, true
	}
	if err := scanner.Err(); err != nil {
		return "", false, fmt.Errorf("read %s: %w", path, err)
	}
	return value, found, nil
}

func parseConfigValue(raw string, shell bool) (string, error) {
	var value strings.Builder
	var quote rune
	escaped := false
	for index, r := range raw {
		if escaped {
			value.WriteRune(r)
			escaped = false
			continue
		}
		if quote != '\'' && r == '\\' {
			escaped = true
			continue
		}
		if quote != 0 {
			if err := appendQuotedConfigRune(&value, &quote, r, raw, shell); err != nil {
				return "", err
			}
			continue
		}
		done, err := appendUnquotedConfigRune(&value, &quote, raw, index, r, shell)
		if err != nil {
			return "", err
		}
		if done {
			break
		}
	}
	if escaped || quote != 0 {
		return "", errors.New("unterminated quote or escape")
	}
	result := strings.TrimSpace(value.String())
	return result, nil
}

func appendQuotedConfigRune(value *strings.Builder, quote *rune, r rune, raw string, shell bool) error {
	if shell && *quote == '"' && (r == '$' || r == '`') {
		return fmt.Errorf("shell expression %q is not supported", raw)
	}
	if r == *quote {
		*quote = 0
		return nil
	}
	value.WriteRune(r)
	return nil
}

func appendUnquotedConfigRune(value *strings.Builder, quote *rune, raw string, index int, r rune, shell bool) (bool, error) {
	if isConfigQuote(r) {
		*quote = r
		return false, nil
	}
	if unsupportedShellExpression(r, shell) {
		return false, fmt.Errorf("shell expression %q is not supported", raw)
	}
	if beginsConfigComment(raw, index, r) {
		return true, nil
	}
	if shellWhitespace(r, shell) {
		return shellWhitespaceEndsValue(raw, index)
	}
	value.WriteRune(r)
	return false, nil
}

func isConfigQuote(r rune) bool { return r == '\'' || r == '"' }

func unsupportedShellExpression(r rune, shell bool) bool {
	if !shell {
		return false
	}
	switch r {
	case '$', '`', ';':
		return true
	default:
		return false
	}
}

func beginsConfigComment(raw string, index int, r rune) bool {
	if r != '#' {
		return false
	}
	if index == 0 {
		return true
	}
	return raw[index-1] == ' ' || raw[index-1] == '\t'
}

func shellWhitespace(r rune, shell bool) bool {
	return shell && (r == ' ' || r == '\t')
}

func shellWhitespaceEndsValue(raw string, index int) (bool, error) {
	if strings.HasPrefix(strings.TrimSpace(raw[index:]), "#") {
		return true, nil
	}
	return false, fmt.Errorf("unquoted whitespace in %q", raw)
}

func splitConfigWords(raw string) ([]string, error) {
	var words []string
	var word strings.Builder
	var quote rune
	escaped := false
	wordStarted := false
	for _, r := range raw {
		if escaped {
			word.WriteRune(r)
			escaped = false
			wordStarted = true
			continue
		}
		if r == '\\' && quote != '\'' {
			escaped = true
			wordStarted = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				word.WriteRune(r)
			}
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			wordStarted = true
			continue
		}
		if r == ' ' || r == '\t' {
			if wordStarted {
				words = append(words, word.String())
				word.Reset()
				wordStarted = false
			}
			continue
		}
		word.WriteRune(r)
		wordStarted = true
	}
	if escaped || quote != 0 {
		return nil, errors.New("unterminated quote or escape")
	}
	if wordStarted {
		words = append(words, word.String())
	}
	return words, nil
}

func fileExists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}
