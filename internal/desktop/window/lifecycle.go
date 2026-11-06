// Package window keeps desktop visibility and core shutdown separate.
package window

type Controller struct {
	Hide func()
	Quit func()
}

// Close handles the title bar action while the core remains supervised.
func (c Controller) Close() {
	c.Hide()
}

// FinishQuit accepts permission only after the core has committed safe exit.
func (c Controller) FinishQuit(allowed bool) bool {
	if !allowed {
		return false
	}
	c.Quit()
	return true
}
