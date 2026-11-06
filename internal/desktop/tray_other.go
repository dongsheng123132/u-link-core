//go:build !darwin && !windows

package desktop

import "log"

func runTray(a *App) error {
	log.Printf("当前平台不支持托盘，退回 headless 模式")
	a.ring.Add("app", "warn", "当前平台不支持托盘，退回后台模式")
	a.waitForSignal()
	return nil
}
