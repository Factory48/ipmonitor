package main

import (
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

// Popup type sizes in DIPs at the designed popupWidth x popupHeight window.
// The system DPI scales DIPs, so only the window's size relative to that
// design (popupScale) changes them.
const (
	popupTitleSize    = 23
	popupTitleMinSize = 15 // a long IPv6 title shrinks to this, then ends in an ellipsis
	popupBodySize     = 17
	popupPadding      = 14
	popupBadgeSize    = 40
	popupColumnGap    = 10
	popupTextGap      = 3
)

// popupScale is the ratio of the actual popup content size (in DIPs) to the
// designed one, bounded so a shrunken window stays legible and a large one
// does not balloon the content.
func popupScale(w, h float32) float32 {
	if w <= 0 || h <= 0 {
		return 1
	}
	return min(max(min(w/popupWidth, h/popupHeight), 0.7), 1.25)
}

func (a *application) popupView(c *ui.Context) {
	t := c.Theme()
	titleColor, textColor := t.Text, t.TextMuted
	text := a.popupText
	if !a.popupDeadline.IsZero() {
		titleColor = t.Success
		label := a.tr("未过期", "Not expired")
		if !time.Now().Before(a.popupDeadline) {
			titleColor = t.Danger
			label = a.tr("已过期", "Expired")
		}
		textColor = titleColor
		text += "\n" + label
	}

	w, h := c.Size()
	k := popupScale(w, h)
	if w <= 0 || h <= 0 {
		w, h = popupWidth, popupHeight
	}
	pad, gap, badge := popupPadding*k, popupColumnGap*k, popupBadgeSize*k
	// Content box: the 1 DIP border and the padding are both inside the window.
	textW, textH := w-2*(pad+1), h-2*(pad+1)
	if a.popupCode != "" {
		textW -= badge + gap
	}

	// The title keeps its size unless it is too wide (long IPv6), when it
	// shrinks to fit down to a readable floor; SingleLine ellipsizes beyond.
	titleSize := popupTitleSize * k
	tw, th := c.MeasureText(0, ui.Span{Text: a.popupTitle, Size: titleSize, Weight: 700})
	if tw > textW && tw > 0 {
		fitted := max(titleSize*textW/tw*0.98, popupTitleMinSize*k)
		th *= fitted / titleSize
		titleSize = fitted
	}

	// The body keeps its size; the lines left under the title go to the last
	// field (the AS name, or a lookup message), the others take one line.
	bodySize := popupBodySize * k
	fields, lineH := strings.Count(text, "\n")+1, float32(0)
	for rest, more := text, true; more; {
		var field string
		field, rest, more = strings.Cut(rest, "\n")
		_, fh := c.MeasureText(0, ui.Span{Text: field, Size: bodySize})
		lineH = max(lineH, fh)
	}
	lines := fields
	if lineH > 0 {
		lines = max(int((textH-th-float32(fields)*popupTextGap*k)/lineH), fields)
	}

	panel := ui.Column(c).Fill().Padding(pad).Gap(7*k).Background(t.Background).Border(1, t.Border).Radius(8 * k)
	panel.OnClick(a.openMain).OnShortcut(0, ui.KeyEscape, func() { a.popup.Hide() })
	panel.Children(func() {
		ui.Row(c).Grow(1).Gap(gap).AlignItems(ui.Center).Children(func() {
			if a.popupCode != "" {
				countryBadge(c, a.popupCode, badge)
			}
			ui.Column(c).Grow(1).Gap(popupTextGap * k).Children(func() {
				ui.Text(c, a.popupTitle).FontSize(titleSize).Bold().SingleLine().TextColor(titleColor)
				for rest, more := text, true; more; {
					var field string
					field, rest, more = strings.Cut(rest, "\n")
					maxLines := 1
					if !more {
						maxLines = lines - (fields - 1)
					}
					ui.Text(c, field).FontSize(bodySize).MaxLines(maxLines).TextColor(textColor)
				}
			})
		})
	})
}
