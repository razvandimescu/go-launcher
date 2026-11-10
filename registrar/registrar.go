// Package registrar provides platform-native login items (start at user login)
// for go-launcher.
package registrar

import launcher "github.com/razvandimescu/go-launcher"

// Config configures the registrar.
type Config struct {
	AppName string // display name and identifier base (e.g. "RINKT Runner")
}

// New returns a platform-native Registrar. On unsupported platforms it
// returns a no-op implementation.
func New(cfg Config) launcher.Registrar {
	return newPlatform(cfg)
}
