package ui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// The activity indicators start hidden; when shown they must get a real
// size, not stay at the 0x0 the grid left them with while hidden.
func TestActivityRowIndicatorIsVisibleWhenActive(t *testing.T) {
	test.NewTempApp(t)
	active := binding.NewBool()
	row := activityRow("RX:", active)
	w := test.NewTempWindow(t, row)
	w.Resize(fyne.NewSize(400, 100))

	indicator := row.Objects[1].(*widget.ProgressBarInfinite)
	for i, on := range []bool{true, false, true} {
		if err := active.Set(on); err != nil {
			t.Fatal(err)
		}
		if indicator.Visible() != on {
			t.Fatalf("step %d: Visible() = %v, want %v", i, indicator.Visible(), on)
		}
		if on && (indicator.Size().Width == 0 || indicator.Size().Height == 0) {
			t.Fatalf("step %d: indicator shown with size %v", i, indicator.Size())
		}
	}
}
