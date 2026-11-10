//go:build !(windows || darwin)

package registrar

type stubRegistrar struct{}

func newPlatform(_ Config) *stubRegistrar { return &stubRegistrar{} }

func (stubRegistrar) RegisterLoginItem(string) error { return nil }
func (stubRegistrar) UnregisterLoginItem() error     { return nil }
