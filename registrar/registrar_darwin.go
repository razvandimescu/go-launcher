//go:build darwin

package registrar

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type darwinRegistrar struct {
	appName string
}

func newPlatform(cfg Config) *darwinRegistrar {
	return &darwinRegistrar{appName: cfg.AppName}
}

func (r *darwinRegistrar) label() string {
	// "RINKT Runner" → "com.rinkt-runner.launcher"
	safe := strings.Map(func(c rune) rune {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '.' {
			return c
		}
		return '-'
	}, r.appName)
	return "com." + strings.ToLower(safe) + ".launcher"
}

func (r *darwinRegistrar) plistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("determine home directory: %w", err)
	}
	return filepath.Join(home, "Library", "LaunchAgents", r.label()+".plist"), nil
}

// escapeXML returns s with XML special characters escaped.
func escapeXML(s string) string {
	var b strings.Builder
	xml.EscapeText(&b, []byte(s))
	return b.String()
}

func (r *darwinRegistrar) RegisterLoginItem(binaryPath string) error {
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<false/>
</dict>
</plist>
`, escapeXML(r.label()), escapeXML(binaryPath))

	path, err := r.plistPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create LaunchAgents dir: %w", err)
	}
	return os.WriteFile(path, []byte(plist), 0644)
}

func (r *darwinRegistrar) UnregisterLoginItem() error {
	path, err := r.plistPath()
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
