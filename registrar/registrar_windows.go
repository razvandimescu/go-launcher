//go:build windows

package registrar

import (
	"fmt"

	"golang.org/x/sys/windows/registry"
)

const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`

type windowsRegistrar struct {
	appName string
}

func newPlatform(cfg Config) *windowsRegistrar {
	return &windowsRegistrar{appName: cfg.AppName}
}

func (r *windowsRegistrar) RegisterLoginItem(binaryPath string) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("open Run key: %w", err)
	}
	defer k.Close()
	return k.SetStringValue(r.appName, binaryPath)
}

func (r *windowsRegistrar) UnregisterLoginItem() error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("open Run key: %w", err)
	}
	defer k.Close()
	err = k.DeleteValue(r.appName)
	if err == registry.ErrNotExist {
		return nil
	}
	return err
}
