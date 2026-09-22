// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"fmt"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
)

// Storage also loads independently for migration destinations, using a separate
// environment prefix so each endpoint can have its own credentials.
type Storage struct {
	Driver, Directory                  string
	Endpoint, Region, Bucket, Prefix   string
	AccessKey, SecretKey, SessionToken string
	PathStyle                          bool
	Timeout                            time.Duration
}

func LoadStorage(prefix, directory string) (Storage, error) {
	c := Storage{
		Driver: getenv(prefix+"STORAGE", "disk"), Directory: getenv(prefix+"OBJECTS_DIR", directory),
		Endpoint: getenv(prefix+"S3_ENDPOINT", ""), Region: getenv(prefix+"S3_REGION", "us-east-1"),
		Bucket: getenv(prefix+"S3_BUCKET", ""), Prefix: strings.Trim(getenv(prefix+"S3_PREFIX", ""), "/"),
		AccessKey: getenv(prefix+"S3_ACCESS_KEY", ""), SecretKey: getenv(prefix+"S3_SECRET_KEY", ""),
		SessionToken: getenv(prefix+"S3_SESSION_TOKEN", ""),
	}
	var err error
	c.PathStyle, err = strconv.ParseBool(getenv(prefix+"S3_PATH_STYLE", "false"))
	if err != nil {
		return c, fmt.Errorf("%sS3_PATH_STYLE must be true or false", prefix)
	}
	c.Timeout, err = time.ParseDuration(getenv(prefix+"S3_TIMEOUT", "2m"))
	if err != nil || c.Timeout <= 0 {
		return c, fmt.Errorf("%sS3_TIMEOUT must be a positive duration", prefix)
	}
	return c, c.Validate()
}

func (c Storage) Validate() error {
	switch c.Driver {
	case "", "disk":
		if c.Directory == "" {
			return fmt.Errorf("disk storage needs an objects directory")
		}
		return nil
	case "s3":
	default:
		return fmt.Errorf("storage must be disk or s3, got %q", c.Driver)
	}
	if c.Bucket == "" || strings.ContainsAny(c.Bucket, "/\\ \t\r\n") {
		return fmt.Errorf("S3 storage needs a valid bucket name")
	}
	if (c.AccessKey == "") != (c.SecretKey == "") {
		return fmt.Errorf("S3 access key and secret key must be set together")
	}
	if c.Prefix != "" && (path.Clean(c.Prefix) != c.Prefix || strings.ContainsAny(c.Prefix, "\\\x00") || c.Prefix == ".." || strings.HasPrefix(c.Prefix, "../")) {
		return fmt.Errorf("S3 prefix must be a relative object path")
	}
	return validateEndpoint(c.Endpoint)
}

func validateEndpoint(endpoint string) error {
	if endpoint == "" {
		return nil
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") ||
		u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return fmt.Errorf("S3 endpoint must be an http(s) origin without credentials, path, query or fragment")
	}
	return nil
}
