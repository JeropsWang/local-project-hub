package window

import "testing"

func TestCloseHidesAndQuitRequiresCorePermission(t *testing.T) {
	hidden, quit := false, false
	controller := Controller{Hide: func() { hidden = true }, Quit: func() { quit = true }}
	controller.Close()
	if !hidden || quit {
		t.Fatal("closing detached the core")
	}
	if controller.FinishQuit(false) || quit {
		t.Fatal("quit bypassed the core")
	}
	if !controller.FinishQuit(true) || !quit {
		t.Fatal("approved quit did not complete")
	}
}
