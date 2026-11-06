//go:build darwin || windows

package desktop

import (
	"fmt"
	"log"
	"os"
	"sync"

	"fyne.io/systray"
)

type trayUI struct {
	app *App

	mu        sync.Mutex
	status    *systray.MenuItem
	devices   *systray.MenuItem
	allow     *systray.MenuItem
	autostart *systray.MenuItem
	iconKey   string
}

func runTray(a *App) error {
	systray.Run(func() { onTrayReady(a) }, func() { a.shutdown() })
	return nil
}

func onTrayReady(a *App) {
	t := &trayUI{app: a}

	// Always show a text label next to the dot.  A 16pt colored dot is
	// easy to miss in a crowded menu bar (users reported "I don't see
	// U-Link"), and the text also survives environments that hide/tint
	// new menu bar images.  Set ULINK_TRAY_TITLE=0 to go icon-only.
	if os.Getenv("ULINK_TRAY_TITLE") == "0" {
		systray.SetTitle("")
	} else {
		systray.SetTitle("U-Link")
	}
	systray.SetTooltip("U-Link 互联 · 后台运行中，点此打开控制台")

	t.status = systray.AddMenuItem("正在连接…", "本机 U-Link 状态")
	t.status.Disable()
	t.devices = systray.AddMenuItem("设备列表…", "在控制台中查看所有设备")
	systray.AddSeparator()
	openItem := systray.AddMenuItem("打开控制台", "在浏览器中打开本地控制台")
	copyItem := systray.AddMenuItem("复制本机 ID", "复制后可直接粘贴到微信")
	systray.AddSeparator()
	t.allow = systray.AddMenuItemCheckbox("允许被控制", "关闭后拒绝远程执行命令", a.snapshotConfig().AllowControl)
	t.autostart = systray.AddMenuItemCheckbox("开机自启", "登录后自动启动 U-Link", autostartEnabledFor(a.snapshotConfig()))
	systray.AddSeparator()
	restartItem := systray.AddMenuItem("重启连线", "重新连接 relay")
	systray.AddSeparator()
	issueItem := systray.AddMenuItem("报告问题（GitHub）", "在浏览器中打开项目仓库提交 Issue")
	prItem := systray.AddMenuItem("提交 PR（GitHub）", "在浏览器中打开项目仓库提交 Pull Request")
	systray.AddSeparator()
	quitItem := systray.AddMenuItem("退出 U-Link", "退出后停止接受远程控制")

	a.setTrayState(true, t.refresh)
	log.Printf("托盘已就绪（菜单栏状态点已创建）")
	t.refresh()

	go func() {
		for {
			select {
			case <-openItem.ClickedCh:
				go a.openDashboard()
			case <-t.devices.ClickedCh:
				go a.openDashboard()
			case <-copyItem.ClickedCh:
				a.copyDeviceID()
			case <-t.allow.ClickedCh:
				go a.toggleAllowControl()
			case <-t.autostart.ClickedCh:
				go a.toggleAutostart()
			case <-restartItem.ClickedCh:
				go a.restartAgent()
			case <-issueItem.ClickedCh:
				go openFeedback(issueURL())
			case <-prItem.ClickedCh:
				go openFeedback(prURL())
			case <-quitItem.ClickedCh:
				a.shutdown()
				systray.Quit()
				return
			}
		}
	}()

	if a.firstRun || a.opts.Open {
		go a.openDashboard()
	}
}

func (t *trayUI) refresh() {
	st := t.app.status()
	state := st.Agent.State

	var statusText string
	switch state {
	case "online":
		statusText = fmt.Sprintf("● 在线 · %s", st.Agent.DeviceID)
	case "paused":
		statusText = fmt.Sprintf("⏸ 已暂停接管 · %s", st.Agent.DeviceID)
	case "connecting":
		statusText = fmt.Sprintf("◌ 连线中… · %s", st.Agent.DeviceID)
	default:
		statusText = fmt.Sprintf("● 离线 · %s", st.Agent.DeviceID)
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	t.status.SetTitle(statusText)
	t.devices.SetTitle(fmt.Sprintf("设备列表 · %d 台在线", st.Controller.DeviceCount))
	if st.Agent.AllowControl {
		t.allow.Check()
	} else {
		t.allow.Uncheck()
	}
	if autostartEnabledFor(t.app.snapshotConfig()) {
		t.autostart.Check()
	} else {
		t.autostart.Uncheck()
	}
	if state != t.iconKey {
		systray.SetIcon(trayIcon(state, st.Agent.AllowControl))
		t.iconKey = state
		log.Printf("托盘状态点: %s", state)
	}
	systray.SetTooltip(fmt.Sprintf("U-Link 互联\n%s\nrelay: %s", statusText, st.Agent.Relay))
}
