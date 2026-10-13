//go:build !(linux && (amd64 || arm64))

package platform

// Rect is a rectangle in MyGo screen coordinates (DIPs).
type Rect struct{ X, Y, Width, Height int }

// PanelBounds has no system panel to report on this OS or architecture.
func PanelBounds(display Rect) (Rect, bool) { return Rect{}, false }
