package main

import "time"

func (a *application) showTimestampPopup(text string, deadline time.Time) {
	if !a.watching || a.popup == nil {
		return
	}
	a.popupTitle = deadline.Local().Format("2006-01-02 15:04:05")
	a.popupText = text
	a.popupCode = ""
	a.popupDeadline = deadline
	a.presentPopup()
}
