// Command u-link is the U-Link desktop agent: a menu bar / tray app that
// keeps a machine reachable through the remote-agent relay and gives the
// operator a local dashboard to control other machines.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/dongsheng123132/remote-agent/internal/desktop"
	"github.com/dongsheng123132/remote-agent/internal/version"
)

func main() {
	var opts desktop.Options
	flag.StringVar(&opts.Relay, "server", "", "relay 地址（默认读取配置；未配置时在控制台「设置」里填写）")
	flag.StringVar(&opts.AgentToken, "token", "", "被控端公开 token（默认读取配置）")
	flag.StringVar(&opts.ControllerToken, "controller-token", "", "控制端私密 token（默认读取配置/设置页）")
	flag.StringVar(&opts.DeviceID, "id", "", "设备 ID（默认自动生成并持久化，如 mac-a1b2）")
	flag.StringVar(&opts.ConfigDir, "config-dir", "", "配置目录（默认 ~/Library/Application Support/U-Link 或 %APPDATA%\\U-Link）")
	flag.IntVar(&opts.Port, "port", 0, "本地控制台端口（默认 17421）")
	flag.BoolVar(&opts.Headless, "headless", false, "不启动托盘，只跑后台服务（调试/测试用）")
	flag.BoolVar(&opts.Open, "open", false, "启动后打开控制台")

	showVersion := flag.Bool("version", false, "打印版本号")
	installApp := flag.Bool("install-app", false, "把当前 .app 安装到 /Applications 并设置开机自启（macOS）")
	printURL := flag.Bool("print-url", false, "打印当前运行实例的控制台地址")
	installAutostart := flag.Bool("install-autostart", false, "写入开机自启项后退出")
	uninstallAutostart := flag.Bool("uninstall-autostart", false, "移除开机自启项后退出")

	flag.Parse()

	if *showVersion {
		fmt.Println(version.Version)
		return
	}

	if *installAutostart || *uninstallAutostart {
		if err := desktop.SetAutostartEnabled(*installAutostart); err != nil {
			fmt.Fprintf(os.Stderr, "设置开机自启失败: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("ok")
		return
	}

	if *installApp {
		if err := desktop.InstallSelf(); err != nil {
			fmt.Fprintf(os.Stderr, "安装失败: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if *printURL {
		url, err := desktop.ConfiguredDashboardURL(opts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "读取控制台地址失败: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(url)
		return
	}

	if err := desktop.Run(opts); err != nil {
		log.Fatalf("U-Link 启动失败: %v", err)
	}
}
