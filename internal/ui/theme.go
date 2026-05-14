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
