package ui

import (
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// tipDelay is how long the pointer rests on a tipButton before its tip shows.
var tipDelay = 500 * time.Millisecond

// tipLayer draws tooltips for tipButtons. Fyne v2.8 has no tooltips, and a
// PopUp would not do: while an overlay is up, hover events only reach the
// overlay, so the button would lose the pointer and the tip would flicker.
// The layer sits on top of the panel's content in a Stack and holds only
// canvas objects, which never take pointer events from the widgets below.
type tipLayer struct {
	box    *fyne.Container // no layout: the bubble is placed by hand
	bubble *fyne.Container
	bg     *canvas.Rectangle
	text   *canvas.Text
	gen    int // bumped on every show/hide so a stale delayed show is dropped
}

func newTipLayer() *tipLayer {
	l := &tipLayer{bg: canvas.NewRectangle(nil), text: canvas.NewText("", nil)}
	l.bubble = container.NewStack(l.bg, container.NewPadded(l.text))
	l.bubble.Hide()
	l.box = container.NewWithoutLayout(l.bubble)
	return l
}

// wrap returns content with the layer stacked on top.
func (l *tipLayer) wrap(content fyne.CanvasObject) fyne.CanvasObject {
	return container.NewStack(content, l.box)
}

// show puts tip under o, right-aligned with it, after tipDelay.
func (l *tipLayer) show(o fyne.CanvasObject, tip string) {
	l.gen++
	gen := l.gen
	place := func() {
		if gen != l.gen {
			return
		}
		d := fyne.CurrentApp().Driver()
		pos := d.AbsolutePositionForObject(o).Subtract(d.AbsolutePositionForObject(l.box))

		l.bg.FillColor = theme.Color(theme.ColorNameOverlayBackground)
		l.bg.StrokeColor = theme.Color(theme.ColorNameInputBorder)
		l.bg.StrokeWidth = 1
		l.bg.CornerRadius = theme.InputRadiusSize()
		l.text.Text = tip
		l.text.Color = theme.Color(theme.ColorNameForeground)
		l.text.TextSize = theme.CaptionTextSize()
		size := l.bubble.MinSize()

		x := pos.X + o.Size().Width - size.Width
		if max := l.box.Size().Width - size.Width; x > max {
			x = max
		}
		if x < 0 {
			x = 0
		}
		l.bubble.Resize(size)
		l.bubble.Move(fyne.NewPos(x, pos.Y+o.Size().Height+theme.Padding()))
		l.bubble.Show()
		l.bubble.Refresh()
	}
	if tipDelay <= 0 {
		place()
		return
	}
	time.AfterFunc(tipDelay, func() { fyne.Do(place) })
}

func (l *tipLayer) hide() {
	l.gen++
	l.bubble.Hide()
}

// tipButton is an icon button that shows a tooltip while hovered.
type tipButton struct {
	widget.Button
	tip   string
	layer *tipLayer
}

var _ desktop.Hoverable = (*tipButton)(nil)

func newTipButton(icon fyne.Resource, tip string, layer *tipLayer, tapped func()) *tipButton {
	b := &tipButton{tip: tip, layer: layer}
	b.Icon = icon
	b.OnTapped = tapped
	b.ExtendBaseWidget(b)
	return b
}

func (b *tipButton) MouseIn(e *desktop.MouseEvent) {
	b.Button.MouseIn(e)
	b.layer.show(b, b.tip)
}

func (b *tipButton) MouseOut() {
	b.Button.MouseOut()
	b.layer.hide()
}
