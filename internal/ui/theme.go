package ui

import (
	"image/color"
	"sync/atomic"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// refconnectTheme provides a clean, modern look using the Inter typeface and
// honours a user-selected light/dark variant override.
type refconnectTheme struct {
	// variant holds "dark", "light", or "" for system default. atomic.Value
	// because the theme's Color method may be called from goroutines other
	// than the one that flips the toggle.
	variant atomic.Value
}

func newRefconnectTheme(mode string) *refconnectTheme {
	t := &refconnectTheme{}
	t.variant.Store(mode)
	return t
}

func (t *refconnectTheme) setVariant(mode string) {
	t.variant.Store(mode)
}

var _ fyne.Theme = (*refconnectTheme)(nil)

func (*refconnectTheme) Font(s fyne.TextStyle) fyne.Resource {
	if s.Monospace {
		return theme.DefaultTheme().Font(s)
	}
	if s.Bold {
		return fontInterBold
	}
	return fontInterRegular
}

func (t *refconnectTheme) Color(n fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	mode, _ := t.variant.Load().(string)
	switch mode {
	case "dark":
		v = theme.VariantDark
	case "light":
		v = theme.VariantLight
	}
	return theme.DefaultTheme().Color(n, v)
}

func (*refconnectTheme) Icon(n fyne.ThemeIconName) fyne.Resource {
	return theme.DefaultTheme().Icon(n)
}

func (*refconnectTheme) Size(n fyne.ThemeSizeName) float32 {
	return theme.DefaultTheme().Size(n)
}

// readableDisabledTheme brightens the disabled text color, for widgets whose
// text must stay readable while locked: the radio port dropdown (disabled
// while connected) and the hint below it (LowImportance, which Fyne draws in
// the disabled color). The stock disabled color is nearly invisible in dark
// mode. Everything else comes from the app theme, so the light/dark toggle
// still applies. Use with container.NewThemeOverride.
type readableDisabledTheme struct{}

var _ fyne.Theme = readableDisabledTheme{}

// readableDisabledMix is how far the disabled color moves toward the
// foreground color: enough to read easily, while still looking dimmer than
// active text.
const readableDisabledMix = 0.6

func (readableDisabledTheme) base() fyne.Theme {
	if app := fyne.CurrentApp(); app != nil {
		return app.Settings().Theme()
	}
	return theme.DefaultTheme()
}

func (t readableDisabledTheme) Color(n fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	base := t.base()
	if n != theme.ColorNameDisabled {
		return base.Color(n, v)
	}
	return mixColor(base.Color(n, v), base.Color(theme.ColorNameForeground, v), readableDisabledMix)
}

func (t readableDisabledTheme) Font(s fyne.TextStyle) fyne.Resource     { return t.base().Font(s) }
func (t readableDisabledTheme) Icon(n fyne.ThemeIconName) fyne.Resource { return t.base().Icon(n) }
func (t readableDisabledTheme) Size(n fyne.ThemeSizeName) float32       { return t.base().Size(n) }

// mixColor returns a blended f of the way from a to b.
func mixColor(a, b color.Color, f float64) color.Color {
	ar, ag, ab, aa := a.RGBA()
	br, bg, bb, ba := b.RGBA()
	mix := func(x, y uint32) uint8 {
		return uint8((float64(x)*(1-f) + float64(y)*f) / 0x101)
	}
	return color.NRGBA{R: mix(ar, br), G: mix(ag, bg), B: mix(ab, bb), A: mix(aa, ba)}
}
