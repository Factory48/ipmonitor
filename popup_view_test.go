package main

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestPopupViewLongContentStaysInsideWindow(t *testing.T) {
	as := strings.Repeat("中国电信集团有限公司自治系统", 10)
	a := &application{
		language:   "zh",
		popupTitle: "2001:0db8:85a3:0000:0000:8a2e:0370:7334",
		popupText:  "中华人民共和国香港特别行政区 (HK)\n" + as,
		popupCode:  "HK",
	}
	for _, size := range [][2]float32{{popupWidth, popupHeight}, {popupWidth * 0.75, popupHeight * 0.75}, {popupWidth * 2, popupHeight * 2}} {
		tt := ui.NewTester(a.popupView, int(size[0]), int(size[1]))
		for _, s := range []string{a.popupTitle, "中华人民共和国香港特别行政区 (HK)", as} {
			r, ok := tt.Find(s)
			if !ok {
				t.Fatalf("%vx%v: popup field %.20q was not rendered", size[0], size[1], s)
			}
			if r.X < 0 || r.Y < 0 || r.X+r.W > size[0]+0.5 || r.Y+r.H > size[1]+0.5 {
				t.Errorf("%vx%v: text %.20q at %+v leaves the window", size[0], size[1], s, r)
			}
		}
	}
}
