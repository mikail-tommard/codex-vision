package input

import (
	"fmt"
	"net/url"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Launch accepts a macOS bundle's main executable and an optional web URL.
// It never accepts a shell command, executable arguments, or a file URL.
func ValidateLaunch(process, rawURL string) error {
	if len(process) > 4096 || !utf8.ValidString(process) || strings.ContainsFunc(process, unicode.IsControl) || !path.IsAbs(process) || path.Clean(process) != process {
		return fmt.Errorf("process must be an absolute app executable path without control characters or traversal")
	}
	bin := path.Dir(process)
	bundle := path.Dir(path.Dir(bin))
	if path.Base(bin) != "MacOS" || path.Base(path.Dir(bin)) != "Contents" || !strings.HasSuffix(bundle, ".app") {
		return fmt.Errorf("process must be the main executable inside an .app/Contents/MacOS bundle")
	}
	if rawURL == "" {
		return nil
	}
	if len(rawURL) > 8192 || !utf8.ValidString(rawURL) {
		return fmt.Errorf("invalid launch URL")
	}
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.Opaque != "" {
		return fmt.Errorf("url must be an absolute http/https URL without credentials")
	}
	return nil
}
