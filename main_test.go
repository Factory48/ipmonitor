package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestMainViewCanReopenSettingsAfterHiddenState(t *testing.T) {
	a := &application{language: "en", config: config{Monitor: true}}
	tt := ui.NewTester(a.view, 820, 500)
	if err := tt.Click("Settings"); err != nil {
		t.Fatal(err)
	}
	if !a.settings {
		t.Fatal("settings did not open")
	}
	tt.Key(0, ui.KeyEscape)
	if a.settings {
		t.Fatal("settings did not close after Escape")
	}
}

func TestPopupViewIncludesCountryResult(t *testing.T) {
	a := &application{language: "en", popupTitle: "8.8.8.8", popupText: "United States (US)\nGoogle LLC", popupCode: "US"}
	tt := ui.NewTester(a.popupView, 380, 144)
	if !tt.HasText("8.8.8.8") || !tt.HasText("Google LLC") {
		t.Fatalf("popup content missing: %v", tt.Texts())
	}
}
