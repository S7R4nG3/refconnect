package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// toggleSwitch is a minimal iOS-style on/off switch: a pill-shaped track with
// a circular thumb that slides between the two ends when tapped. Fyne v2.7
// doesn't ship one, so we draw it with canvas primitives.
type toggleSwitch struct {
	widget.BaseWidget

	checked   bool
	onChanged func(bool)
}

func newToggleSwitch(checked bool, onChanged func(bool)) *toggleSwitch {
	t := &toggleSwitch{checked: checked, onChanged: onChanged}
	t.ExtendBaseWidget(t)
	return t
}

func (t *toggleSwitch) Tapped(_ *fyne.PointEvent) {
	t.checked = !t.checked
	t.Refresh()
	if t.onChanged != nil {
		t.onChanged(t.checked)
	}
}

func (t *toggleSwitch) SetChecked(c bool) {
	if t.checked == c {
		return
	}
	t.checked = c
	t.Refresh()
}

func (t *toggleSwitch) CreateRenderer() fyne.WidgetRenderer {
	r := &toggleSwitchRenderer{
		toggle: t,
		track:  canvas.NewRectangle(color.Transparent),
		thumb:  canvas.NewCircle(color.Transparent),
	}
	r.track.CornerRadius = 10
	r.applyColors()
	return r
}

type toggleSwitchRenderer struct {
	toggle *toggleSwitch
	track  *canvas.Rectangle
	thumb  *canvas.Circle
}

// Fixed visual dimensions for the switch. The container may allocate more
// space (e.g. an HBox row sized to a taller label), so we always draw at
// these dimensions and centre inside the allocated area.
const (
	switchW     = float32(20)
	switchH     = float32(10)
	switchInset = float32(1.5)
)

func (r *toggleSwitchRenderer) MinSize() fyne.Size {
	return fyne.NewSize(switchW, switchH)
}

func (r *toggleSwitchRenderer) Layout(s fyne.Size) {
	offX := (s.Width - switchW) / 2
	offY := (s.Height - switchH) / 2
	if offX < 0 {
		offX = 0
	}
	if offY < 0 {
		offY = 0
	}

	r.track.Resize(fyne.NewSize(switchW, switchH))
	r.track.Move(fyne.NewPos(offX, offY))
	r.track.CornerRadius = switchH / 2

	d := switchH - switchInset*2
	r.thumb.Resize(fyne.NewSize(d, d))
	if r.toggle.checked {
		r.thumb.Move(fyne.NewPos(offX+switchW-d-switchInset, offY+switchInset))
	} else {
		r.thumb.Move(fyne.NewPos(offX+switchInset, offY+switchInset))
	}
}

func (r *toggleSwitchRenderer) Refresh() {
	r.applyColors()
	r.Layout(r.toggle.Size())
	canvas.Refresh(r.toggle)
}

func (r *toggleSwitchRenderer) applyColors() {
	if r.toggle.checked {
		r.track.FillColor = theme.Color(theme.ColorNamePrimary)
		r.thumb.FillColor = color.White
	} else {
		r.track.FillColor = theme.Color(theme.ColorNameInputBackground)
		r.thumb.FillColor = theme.Color(theme.ColorNameForeground)
	}
}

func (r *toggleSwitchRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.track, r.thumb}
}

func (r *toggleSwitchRenderer) Destroy() {}
