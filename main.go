package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/Factory48/ipmonitor/internal/core"
	"github.com/Factory48/ipmonitor/internal/platform"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// trayPNG is the original IP badge from the C++ program, not a generic
// replacement icon.
//
//go:embed resources/tray.png
var trayPNG []byte

const (
	appName       = "IPMonitor"
	popupDuration = 5 * time.Second
)

var appVersion = "1.0.0"

type config struct {
	Token    string `json:"token"`
	Monitor  bool   `json:"monitor"`
	Language string `json:"language"`
}

type historyEntry struct {
	Info    core.Info
	Fetched time.Time
	Seen    time.Time
}

type lookupJob struct {
	IP     string
	Token  string
	ID     uint64
	Notify bool
}

type lookupDone struct {
	Job   lookupJob
	Info  core.Info
	Error error
}

type application struct {
	win              *mygo.Window
	popup            *mygo.Window
	tray             *mygo.Tray
	stopClip         func()
	config           config
	configDir        string
	language         string
	query            string
	status           string
	entries          []historyEntry
	list             ui.ListState
	selected         int
	languageChoice   string
	languageDraft    string
	tokenDraft       string
	startup          bool
	startupDraft     bool
	quitting         bool
	settings         bool
	pending          *lookupJob
	latestID         uint64
	watching         bool
	popupText        string
	popupTitle       string
	popupCode        string
	popupTimer       *time.Timer
	popupDeadline    time.Time
	popupExpiryTimer *time.Timer

	mu       sync.Mutex
	wake     *sync.Cond
	stopping bool
	results  chan lookupDone
	worker   *core.Client
}

func (a *application) chinese() bool {
	return a.language == "zh" || (a.language == "system" && strings.HasPrefix(strings.ToLower(mygo.App.Locale()), "zh"))
}

func (a *application) tr(zh, en string) string {
	if a.chinese() {
		return zh
	}
	return en
}

func (a *application) saveConfig() {
	if a.configDir == "" {
		return
	}
	a.config.Language = a.language
	a.config.Monitor = a.watching
	data, err := json.MarshalIndent(a.config, "", "  ")
	if err == nil {
		_ = os.WriteFile(filepath.Join(a.configDir, "settings.json"), data, 0o600)
	}
}

func (a *application) loadConfig() {
	dir, err := mygo.App.Path(mygo.PathUserData)
	if err == nil {
		a.configDir = dir
	}
	if a.configDir != "" {
		if data, err := os.ReadFile(filepath.Join(a.configDir, "settings.json")); err == nil {
			_ = json.Unmarshal(data, &a.config)
		}
	}
	if a.config.Language != "zh" && a.config.Language != "en" {
		a.config.Language = "system"
	}
	a.language = a.config.Language
	if !a.config.Monitor && a.config.Token == "" {
		// A zero-value config is the first launch; monitoring is enabled by default.
		a.config.Monitor = true
	}
	a.watching = a.config.Monitor
	a.startup = mygo.App.OpenAtLogin()
}

func (a *application) setStatus(s string) {
	a.status = s
}

func (a *application) view(c *ui.Context) {
	if c.Shortcut(0, ui.KeyEscape) && a.settings {
		a.settings = false
	}
	if a.settings {
		a.settingsView(c)
	} else {
		a.mainView(c)
	}
}

func (a *application) mainView(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Fill().Padding(18).Gap(12).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).Gap(3).Children(func() {
				ui.Text(c, a.tr("IPMonitor", "IPMonitor")).FontSize(23).Bold()
				ui.Text(c, a.tr("识别公网 IP 与完整的 13 位毫秒时间戳", "Recognizes public IPs and exact 13-digit millisecond timestamps")).FontSize(12).TextColor(t.TextMuted)
			})
			ui.Button(c, a.tr("设置", "Settings")).OnClick(a.openSettings)
		})
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			if ui.TextInput(c.Key("query"), &a.query).Grow(1).
				Placeholder(a.tr("IP / http(s)://IP（支持端口和路径）", "IP / http(s)://IP (ports and paths supported)")).
				Label(a.tr("查询 IP", "IP to look up")).Submitted() {
				a.submit(a.query, false)
			}
			if ui.PrimaryButton(c, a.tr("查询", "Look up")).Clicked() {
				a.submit(a.query, false)
			}
		})
		if a.status == "" {
			a.status = a.tr("就绪 · 历史最多 100 条", "Ready · Up to 100 history entries")
		}
		ui.Text(c, a.status).FontSize(12).TextColor(t.TextMuted).SingleLine()
		columns := []ui.TableColumn{
			{Title: "IP", Width: 210},
			{Title: a.tr("国家 / 地区", "Country / Region"), Width: 160},
			{Title: a.tr("AS 名称", "AS name")},
			{Title: a.tr("时间", "Time"), Width: 90},
		}
		if len(a.entries) == 0 {
			ui.Column(c).Grow(1).Center().Children(func() {
				ui.Text(c, a.tr("暂无查询记录", "No lookups yet")).TextColor(t.TextMuted)
			})
		} else {
			if a.list.Selected == nil {
				a.list.Selected = &a.selected
			}
			if ui.Table(c, &a.list, columns, len(a.entries), func(row, col int) {
				e := a.entries[row]
				switch col {
				case 0:
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						countryBadge(c, e.Info.CountryCode, 24)
						ui.Text(c, e.Info.IP).SingleLine()
					})
				case 1:
					ui.Text(c, countryText(e.Info)).SingleLine()
				case 2:
					ui.Text(c, displayOr(e.Info.ASName, a.tr("未知 / 未公布", "Unknown / unpublished"))).SingleLine()
				case 3:
					ui.Text(c, e.Seen.Local().Format("15:04:05")).SingleLine()
				}
			}).Grow(1).Submitted() {
				if a.selected >= 0 && a.selected < len(a.entries) {
					a.query = a.entries[a.selected].Info.IP
				}
			}
		}
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			if ui.Checkbox(c, &a.watching, a.tr("监控剪贴板", "Monitor clipboard")).Changed() {
				a.setWatching(a.watching)
			}
			ui.Spacer(c)
			if ui.Button(c, a.tr("清空历史", "Clear history")).Disabled(len(a.entries) == 0).Clicked() {
				a.clearHistory()
			}
			ui.Button(c, a.tr("退出", "Quit")).OnClick(mygo.App.Quit)
		})
	})
}

func (a *application) openSettings() {
	a.languageDraft = a.language
	a.languageChoice = a.languageLabelFor(a.languageDraft)
	a.tokenDraft = a.config.Token
	a.startupDraft = a.startup
	a.settings = true
}

func (a *application) languageLabelFor(language string) string {
	switch language {
	case "zh":
		return "中文"
	case "en":
		return "English"
	default:
		return a.tr("跟随系统", "System default")
	}
}

func (a *application) settingsView(c *ui.Context) {
	t := c.Theme()
	options := []string{"system", "zh", "en"}
	labels := []string{a.tr("跟随系统", "System default"), "中文", "English"}
	if a.languageChoice == "" {
		a.languageChoice = a.languageLabelFor(a.languageDraft)
	}
	ui.Column(c).Fill().Padding(22).Gap(14).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Children(func() {
			ui.Button(c, "←").Label(a.tr("返回", "Back")).OnClick(func() { a.settings = false })
			ui.Text(c, a.tr("设置", "Settings")).FontSize(22).Bold().Margin(0, 12, 0, 0)
		})
		ui.Text(c, a.tr("IPinfo Lite Token（仅发送到 api.ipinfo.io）", "IPinfo Lite Token (sent only to api.ipinfo.io)")).FontSize(12).TextColor(t.TextMuted)
		ui.TextInput(c.Key("token"), &a.tokenDraft).Password().Placeholder(a.tr("输入 Token", "Enter token")).Label(a.tr("Token", "Token"))
		ui.Checkbox(c, &a.startupDraft, a.tr("开机自启动（当前用户）", "Start automatically when I sign in"))
		ui.Select(c, &a.languageChoice, labels).Label(a.tr("语言", "Language")).OnChange(func() {
			for i, label := range labels {
				if a.languageChoice == label {
					a.languageDraft = options[i]
					a.languageChoice = a.languageLabelFor(a.languageDraft)
					break
				}
			}
		})
		ui.Text(c, a.tr("Token 以明文保存在用户配置目录；历史和缓存只保存在内存。", "The token is stored as plain text in the user config directory; history and cache stay in memory.")).FontSize(12).TextColor(t.TextMuted)
		ui.Spacer(c)
		ui.Row(c).Gap(8).Children(func() {
			ui.Spacer(c)
			ui.Button(c, a.tr("取消", "Cancel")).OnClick(func() { a.settings = false })
			ui.PrimaryButton(c, a.tr("保存", "Save")).OnClick(func() {
				wasStartup := mygo.App.OpenAtLogin()
				a.language = a.languageDraft
				a.config.Token = a.tokenDraft
				a.startup = a.startupDraft
				a.saveConfig()
				if a.startup != wasStartup {
					if err := mygo.App.SetOpenAtLogin(a.startup); err != nil {
						a.status = err.Error()
					}
				}
				a.settings = false
				a.status = a.tr("设置已保存", "Settings saved")
			})
		})
	})
}

func countryText(info core.Info) string {
	if info.CountryCode == "" || info.CountryCode == "--" {
		return displayOr(info.Country, "--")
	}
	return displayOr(info.Country, "--") + " (" + info.CountryCode + ")"
}

func displayOr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func (a *application) submit(text string, notify bool) {
	if notify {
		if deadline, ok := core.ParseTimestamp(text); ok {
			a.latestID++
			a.cancelPending()
			a.showTimestampPopup(text, deadline)
			return
		}
	}
	addr, err := core.ParseIP(text)
	if err != nil {
		if !notify {
			a.status = a.tr("开头须为 IP、http://IP 或 https://IP；IPv6 端口需方括号。", "Start with IP, http://IP or https://IP; bracket IPv6 ports.")
		}
		return
	}
	ip := addr.String()
	if !core.IsPublic(addr) {
		if !notify {
			a.status = a.tr("已忽略非公网 IP，不会发送请求。", "Non-public IP ignored; no request sent.")
		}
		return
	}
	a.latestID++
	a.cancelPending()
	now := time.Now()
	for _, e := range a.entries {
		if e.Info.IP == ip && now.Sub(e.Fetched) < time.Hour {
			a.status = ip + a.tr(" · 本地缓存", " · Local cache")
			a.showResult(e.Info, notify)
			a.remember(e.Info, e.Fetched)
			return
		}
	}
	if a.config.Token == "" {
		a.status = a.tr("请先在设置中填写 IPinfo Token。", "Enter an IPinfo token in Settings first.")
		return
	}
	a.status = a.tr("正在查询 ", "Looking up ") + ip + " …"
	a.showPopup(ip, a.tr("正在查询…", "Looking up..."), "", notify)
	job := lookupJob{IP: ip, Token: a.config.Token, ID: a.latestID, Notify: notify}
	a.mu.Lock()
	a.pending = &job
	a.mu.Unlock()
	a.wake.Signal()
}

func (a *application) cancelPending() {
	a.mu.Lock()
	a.pending = nil
	a.mu.Unlock()
}

func (a *application) workerLoop() {
	next := time.Now()
	for {
		a.mu.Lock()
		for !a.stopping && a.pending == nil {
			a.wake.Wait()
		}
		if a.stopping {
			a.mu.Unlock()
			return
		}
		wait := time.Until(next)
		if wait > 0 {
			a.mu.Unlock()
			time.Sleep(wait)
			continue
		}
		job := *a.pending
		a.pending = nil
		a.mu.Unlock()
		next = time.Now().Add(200 * time.Millisecond)
		info, err := a.worker.Lookup(context.Background(), job.IP, job.Token)
		a.results <- lookupDone{Job: job, Info: info, Error: err}
	}
}

func (a *application) handleDone(done lookupDone) {
	if done.Job.ID != a.latestID {
		return
	}
	if done.Error != nil {
		message := a.lookupError(done.Error)
		a.status = done.Job.IP + a.tr(" · ", " · ") + message
		a.showPopup(done.Job.IP, message, "", done.Job.Notify)
		return
	}
	a.remember(done.Info, time.Now())
	a.status = done.Job.IP + a.tr(" · 查询完成", " · Lookup complete")
	a.showResult(done.Info, done.Job.Notify)
}

func (a *application) lookupError(err error) string {
	switch {
	case errors.Is(err, core.ErrInvalidIP):
		return a.tr("无效的 IP 地址", "Invalid IP address")
	case errors.Is(err, core.ErrNonPublic):
		return a.tr("非公网 IP，已跳过查询", "Non-public IP address; lookup skipped")
	case errors.Is(err, core.ErrMissingToken):
		return a.tr("缺少 IPinfo Token", "Missing IPinfo token")
	case errors.Is(err, core.ErrInvalidToken):
		return a.tr("Token 格式无效", "Invalid token")
	case errors.Is(err, core.ErrResponseTooLarge):
		return a.tr("响应超过 64 KiB", "Response exceeds 64 KiB")
	case errors.Is(err, core.ErrInvalidUTF8):
		return a.tr("响应不是有效的 UTF-8", "Response is not valid UTF-8")
	case errors.Is(err, core.ErrInvalidJSON):
		return a.tr("响应不是有效的 JSON", "Invalid JSON response")
	case errors.Is(err, core.ErrAPIError):
		return a.tr("IPinfo 返回错误", "IPinfo returned an error")
	default:
		var status *core.HTTPError
		if errors.As(err, &status) {
			return fmt.Sprintf("HTTP %d", status.StatusCode)
		}
		return err.Error()
	}
}

func (a *application) remember(info core.Info, fetched time.Time) {
	out := a.entries[:0]
	for _, e := range a.entries {
		if e.Info.IP != info.IP {
			out = append(out, e)
		}
	}
	a.entries = append([]historyEntry{{Info: info, Fetched: fetched, Seen: time.Now()}}, out...)
	if len(a.entries) > 100 {
		a.entries = a.entries[:100]
	}
}

func (a *application) clearHistory() {
	a.latestID++
	a.cancelPending()
	a.entries = nil
	a.status = a.tr("历史与缓存已清空。", "History and cache cleared.")
}

func (a *application) setWatching(enabled bool) {
	a.watching = enabled
	if a.stopClip != nil {
		a.stopClip()
		a.stopClip = nil
	}
	if enabled {
		stop, err := platform.WatchClipboard(func(text string) {
			if a.watching {
				a.submit(text, true)
			}
		})
		if err != nil {
			a.watching = false
			a.status = err.Error()
		} else {
			a.stopClip = stop
		}
	}
	a.saveConfig()
	if a.tray != nil {
		a.tray.SetToolTip(a.trayTip())
	}
}

func (a *application) trayTip() string {
	return appName + " · " + a.tr(a.watchLabel(), "Monitoring clipboard")
}

func (a *application) watchLabel() string {
	if a.watching {
		return "剪贴板监控中"
	}
	return "已暂停"
}

func (a *application) showResult(info core.Info, notify bool) {
	if notify && a.watching {
		a.showPopup(info.IP, countryText(info)+"\n"+displayOr(info.ASName, a.tr("未知 / 未公布", "Unknown / unpublished")), info.CountryCode, true)
	}
}

func (a *application) showPopup(title, text, code string, notify bool) {
	if !notify || !a.watching || a.popup == nil {
		return
	}
	a.popupTitle, a.popupText, a.popupCode = title, text, code
	a.popupDeadline = time.Time{}
	a.presentPopup()
}

func (a *application) presentPopup() {
	a.popup.Update(func() {})
	a.positionPopup()
	a.popup.ShowInactive()
	if a.popupTimer != nil {
		a.popupTimer.Stop()
	}
	a.popupTimer = time.AfterFunc(popupDuration, func() {
		mygo.RunOnMain(func() {
			if a.popup != nil {
				a.popup.Hide()
			}
		})
	})
	if a.popupExpiryTimer != nil {
		a.popupExpiryTimer.Stop()
		a.popupExpiryTimer = nil
	}
	if !a.popupDeadline.IsZero() {
		deadline := a.popupDeadline
		if delay := time.Until(deadline); delay > 0 && delay < popupDuration {
			a.popupExpiryTimer = time.AfterFunc(delay, func() {
				mygo.RunOnMain(func() {
					if a.popup != nil && a.popupDeadline.Equal(deadline) && a.popup.IsVisible() {
						a.popup.Update(func() {})
					}
				})
			})
		}
	}
}

func (a *application) ready() {
	a.loadConfig()
	ensureAutostart()
	a.worker = core.NewClient()
	a.results = make(chan lookupDone, 8)
	a.wake = sync.NewCond(&a.mu)
	go a.workerLoop()
	go func() {
		for done := range a.results {
			mygo.RunOnMain(func() { a.handleDone(done) })
		}
	}()
	a.createMainWindow()
	a.popup = mygo.NewWindow(mygo.WindowOptions{
		Title: appName, Width: popupWidth, Height: popupHeight, Hidden: true,
		Frameless: true, AlwaysOnTop: true, SkipTaskbar: true,
		DisableResize: true, DisableMinimize: true, DisableMaximize: true,
		DisableFullScreen: true, Content: ui.View(a.popupView),
	})
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) { a.quitting = true })
	mygo.App.OnWindowAllClosed(func() {})
	mygo.App.OnActivate(func(hasVisibleWindows bool) {
		if !hasVisibleWindows {
			a.openMain()
		}
	})
	menu := mygo.NewMenu([]*mygo.MenuItem{
		{Label: a.tr("打开 IPMonitor", "Open IPMonitor"), Click: func(*mygo.MenuItem, *mygo.Window) { a.openMain() }},
		{ID: "monitor", Type: mygo.MenuItemCheckbox, Label: a.tr("监控剪贴板", "Monitor clipboard"), Checked: a.watching, Click: func(item *mygo.MenuItem, _ *mygo.Window) { a.setWatching(item.Checked) }},
		mygo.Separator(),
		{Role: mygo.RoleQuit},
	})
	var err error
	a.tray, err = mygo.NewTray(mygo.TrayOptions{Icon: trayIcon(), ToolTip: a.trayTip()})
	if err == nil {
		if runtime.GOOS == "linux" {
			a.tray.SetMenu(menu)
		} else {
			a.tray.OnClick(a.openMain)
			a.tray.OnRightClick(func() { a.tray.SetMenu(menu); a.tray.PopUpMenu(); a.tray.SetMenu(nil) })
		}
	} else {
		log.Printf("tray unavailable: %v", err)
		a.openMain()
	}
	if a.watching {
		a.setWatching(true)
	}
	mygo.App.OnSecondInstance(func([]string, string) { a.openMain() })
	mygo.App.OnWillQuit(func(*mygo.QuitEvent) {
		if a.stopClip != nil {
			a.stopClip()
		}
		a.mu.Lock()
		a.stopping = true
		a.pending = nil
		a.mu.Unlock()
		a.wake.Broadcast()
		if a.popupTimer != nil {
			a.popupTimer.Stop()
		}
		if a.popupExpiryTimer != nil {
			a.popupExpiryTimer.Stop()
		}
		if a.tray != nil {
			a.tray.Destroy()
		}
	})
}

func trayIcon() []byte { return trayPNG }

func main() {
	if !mygo.App.RequestSingleInstanceLock() {
		return
	}
	mygo.App.SetName(appName)
	mygo.App.SetVersion(appVersion)
	a := &application{language: "system", config: config{Monitor: true}}
	if runtime.GOOS == "linux" && os.Getenv("WAYLAND_DISPLAY") != "" && os.Getenv("DISPLAY") != "" {
		// GTK clipboard owner-change is global on X11/XWayland; native Wayland
		// clients can only read the focused surface's selection.
		_ = os.Setenv("GDK_BACKEND", "x11")
	}
	mygo.App.WhenReady(a.ready)
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
