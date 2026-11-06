#!/usr/bin/env bash
# ============================================================
# uninstall-link-mac.sh — 卸载 macOS 版 U-Link
#
# 用法：
#   ./scripts/uninstall-link-mac.sh                  # 卸载程序，保留配置
#   ./scripts/uninstall-link-mac.sh --purge          # 连配置一起删
#   ./scripts/uninstall-link-mac.sh --dry-run        # 只演练，不动手
#   ./scripts/uninstall-link-mac.sh --app /path/U-Link.app
#   ./scripts/uninstall-link-mac.sh --config-dir ~/somewhere
#   ./scripts/uninstall-link-mac.sh --help
#
# 顺序是有讲究的（见 docs/DESKTOP-APP.md §2）：
#   1. 先 launchctl bootout gui/${UID}/org.uclaw.u-link
#      （失败再 launchctl unload -w ~/Library/LaunchAgents/org.uclaw.u-link.plist）
#   2. 再删 LaunchAgent plist
#   3. 最后删 .app
# 反过来做的话，launchd 下次登录会尝试拉起一个已经不存在的程序，
# 在 Console /「系统设置 → 登录项」里留下报错。
#
# 默认保留配置；--purge 才删：
#   ~/Library/Application Support/U-Link
#   ~/Library/Logs/U-Link
#   （以及 org.uclaw.u-link 的偏好设置 / 缓存 / Saved State）
# ============================================================
set -euo pipefail

# 一切路径以仓库根为基准（脚本可能在任意 cwd 被调用）
cd "$(dirname "$0")/.."
REPO_ROOT="$(pwd)"

BUNDLE_ID="org.uclaw.u-link"
LAUNCH_AGENT_LABEL="org.uclaw.u-link"
USER_LAUNCH_AGENT="${HOME}/Library/LaunchAgents/${LAUNCH_AGENT_LABEL}.plist"
SYSTEM_LAUNCH_AGENT="/Library/LaunchAgents/${LAUNCH_AGENT_LABEL}.plist"
APP_SUPPORT_DIR="${HOME}/Library/Application Support/U-Link"
LOG_DIR="${HOME}/Library/Logs/U-Link"
# 应用本身支持 ULINK_CONFIG_DIR 覆盖配置目录（见 internal/desktop/config.go），
# 如果当前 shell 设了它，--purge 时一并纳入清理。
ENV_CONFIG_DIR="${ULINK_CONFIG_DIR:-}"
BIN_NAME="u-link"

PURGE=0
DRY_RUN=0
EXTRA_APPS=""
EXTRA_CONFIG_DIRS=""
FAILURES=0
UID_NUM="$(id -u)"

say() { printf '%s\n' "$*"; }
note() { printf '  [..]  %s\n' "$*"; }
ok() { printf '  [OK]  %s\n' "$*"; }
warn() { printf '  [!!]  %s\n' "$*"; }
die() {
    printf '\n[FAIL] %s\n' "$*" >&2
    exit 1
}

usage() {
    cat <<'USAGE'
用法: ./scripts/uninstall-link-mac.sh [选项]

默认行为：停止 LaunchAgent、删 LaunchAgent plist、删 U-Link.app，保留配置。

选项：
  --purge               连配置一起删（Application Support / Logs / Preferences）
  --dry-run, -n         只打印将要做什么，不实际删除
  --app <path>          额外指定要删的 .app 路径（可重复）
  --config-dir <path>   --purge 时额外删除的配置目录（可重复）
  -h, --help            显示本帮助

默认会检查的 .app 位置：
  /Applications/U-Link.app
  ~/Applications/U-Link.app
  <仓库>/build/U-Link.app

退出码：0 成功或本来就没有；1 有删除动作失败（会逐条列出）。
USAGE
}

# ---- 参数 ------------------------------------------------------------------
while [ "$#" -gt 0 ]; do
    case "$1" in
        --purge)
            PURGE=1
            ;;
        --dry-run|-n)
            DRY_RUN=1
            ;;
        --app)
            shift
            [ "$#" -gt 0 ] || die "--app 后面缺少路径"
            EXTRA_APPS="${EXTRA_APPS}$1"$'\n'
            ;;
        --config-dir)
            shift
            [ "$#" -gt 0 ] || die "--config-dir 后面缺少路径"
            EXTRA_CONFIG_DIRS="${EXTRA_CONFIG_DIRS}$1"$'\n'
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            die "未知参数：$1（用 --help 查看用法）"
            ;;
    esac
    shift
done

say "=============================================="
say "  U-Link · macOS 卸载"
say "=============================================="
if [ "${PURGE}" -eq 1 ]; then
    say "  模式：卸载 + 删除配置（--purge）"
else
    say "  模式：只卸载程序，保留配置"
fi
if [ "${DRY_RUN}" -eq 1 ]; then
    say "  演练：dry-run，不会真正删除任何东西"
fi
say ""

# ---- 第 1 步：停掉 LaunchAgent ---------------------------------------------
# 必须在删 plist / .app 之前。bootout 失败（未加载 / 老系统）再退回 unload。
say "[1/4] 停止并注销 LaunchAgent（${LAUNCH_AGENT_LABEL}）"
if [ "${DRY_RUN}" -eq 1 ]; then
    note "[dry-run] launchctl bootout gui/${UID_NUM}/${LAUNCH_AGENT_LABEL}"
    note "[dry-run] launchctl unload -w \"${USER_LAUNCH_AGENT}\"（bootout 失败时）"
else
    BOOTOUT_DONE=0
    if launchctl bootout "gui/${UID_NUM}/${LAUNCH_AGENT_LABEL}" >/dev/null 2>&1; then
        BOOTOUT_DONE=1
        ok "launchctl bootout gui/${UID_NUM}/${LAUNCH_AGENT_LABEL}"
    fi
    if [ "${BOOTOUT_DONE}" -eq 0 ]; then
        if [ -f "${USER_LAUNCH_AGENT}" ] && launchctl unload -w "${USER_LAUNCH_AGENT}" >/dev/null 2>&1; then
            BOOTOUT_DONE=1
            ok "launchctl unload -w ${USER_LAUNCH_AGENT}"
        fi
    fi
    if [ "${BOOTOUT_DONE}" -eq 0 ]; then
        # 未加载是正常情况（用户从没开过自启），不算失败
        note "LaunchAgent 未加载或不存在，跳过（这不影响后续卸载）"
    fi
    # 老版本 launchd 的 label 兜底清理
    launchctl remove "${LAUNCH_AGENT_LABEL}" >/dev/null 2>&1 || true
fi

# ---- 第 2 步：退出正在运行的进程 -------------------------------------------
say ""
say "[2/4] 退出正在运行的 U-Link"
# 匹配完整路径，避免误伤其它名字里有 u-link 的进程
PROC_PATTERN="U-Link.app/Contents/MacOS/${BIN_NAME}"
if [ "${DRY_RUN}" -eq 1 ]; then
    note "[dry-run] osascript -e 'tell application \"U-Link\" to quit' + pkill -f \"${PROC_PATTERN}\""
elif pgrep -f "${PROC_PATTERN}" >/dev/null 2>&1; then
    osascript -e 'tell application "U-Link" to quit' >/dev/null 2>&1 || true
    i=0
    while [ "${i}" -lt 10 ]; do
        pgrep -f "${PROC_PATTERN}" >/dev/null 2>&1 || break
        sleep 0.5
        i=$((i + 1))
    done
    if pgrep -f "${PROC_PATTERN}" >/dev/null 2>&1; then
        warn "菜单栏「退出」未生效，发送 SIGTERM"
        pkill -f "${PROC_PATTERN}" >/dev/null 2>&1 || true
        sleep 1
    fi
    if pgrep -f "${PROC_PATTERN}" >/dev/null 2>&1; then
        warn "进程仍在运行，请在菜单栏图标里点「退出」后重跑本脚本"
    else
        ok "U-Link 已退出"
    fi
else
    note "没有运行中的 U-Link"
fi

# ---- 删除辅助 --------------------------------------------------------------
# 注意：不要用数组 + set -u，macOS 自带 bash 3.2 对空数组展开会报 unbound variable
remove_path() {
    path="$1"
    label="$2"
    if [ ! -e "${path}" ] && [ ! -L "${path}" ]; then
        return 0
    fi
    if [ "${DRY_RUN}" -eq 1 ]; then
        note "[dry-run] 删除${label}：${path}"
        return 0
    fi
    if rm -rf "${path}" 2>/dev/null; then
        ok "已删除${label}：${path}"
        return 0
    fi
    warn "删除失败（可能权限不足）：${path}"
    case "${path}" in
        /Applications/*|/Library/*)
            note "需要管理员权限，可手动执行：sudo rm -rf \"${path}\""
            ;;
    esac
    FAILURES=$((FAILURES + 1))
    return 0
}

# ---- 第 3 步：删 LaunchAgent plist 与 .app ---------------------------------
say ""
say "[3/4] 删除 LaunchAgent 配置与 .app"

# 3a. LaunchAgent plist（bootout 已在上一步完成）
remove_path "${USER_LAUNCH_AGENT}" " LaunchAgent"
if [ -f "${SYSTEM_LAUNCH_AGENT}" ]; then
    warn "发现系统级 LaunchAgent：${SYSTEM_LAUNCH_AGENT}（需要 sudo 才能删）"
    note "如需清理：sudo launchctl bootout system/${LAUNCH_AGENT_LABEL}; sudo rm -f \"${SYSTEM_LAUNCH_AGENT}\""
fi

# 3b. .app 候选位置：默认三个 + --app 追加
APPS_TO_CHECK="/Applications/U-Link.app
${HOME}/Applications/U-Link.app
${REPO_ROOT}/build/U-Link.app"

if [ -n "${EXTRA_APPS}" ]; then
    while IFS= read -r extra; do
        if [ -n "${extra}" ]; then
            APPS_TO_CHECK="${APPS_TO_CHECK}
${extra}"
        fi
    done <<< "${EXTRA_APPS}"
fi

FOUND_APP=0
while IFS= read -r app; do
    if [ -n "${app}" ] && { [ -e "${app}" ] || [ -L "${app}" ]; }; then
        FOUND_APP=1
        remove_path "${app}" " .app"
    fi
done <<< "${APPS_TO_CHECK}"
if [ "${FOUND_APP}" -eq 0 ]; then
    note "没有找到 U-Link.app（/Applications、~/Applications、build/ 均无）"
    note "如果装在别处，用 --app /path/to/U-Link.app 指定"
fi

# ---- 第 4 步：配置 ---------------------------------------------------------
say ""
if [ "${PURGE}" -eq 1 ]; then
    say "[4/4] 删除配置与日志（--purge）"
    CONFIG_TO_CHECK="${APP_SUPPORT_DIR}
${LOG_DIR}
${HOME}/Library/Caches/${BUNDLE_ID}
${HOME}/Library/Caches/U-Link
${HOME}/Library/Saved Application State/${BUNDLE_ID}.savedState
${HOME}/Library/Preferences/${BUNDLE_ID}.plist"

    if [ -n "${EXTRA_CONFIG_DIRS}" ]; then
        while IFS= read -r extra; do
            if [ -n "${extra}" ]; then
                CONFIG_TO_CHECK="${CONFIG_TO_CHECK}
${extra}"
            fi
        done <<< "${EXTRA_CONFIG_DIRS}"
    fi

    # 应用支持 ULINK_CONFIG_DIR 环境变量重定向配置目录，设了就一起删
    if [ -n "${ENV_CONFIG_DIR}" ] && [ -d "${ENV_CONFIG_DIR}" ]; then
        CONFIG_TO_CHECK="${CONFIG_TO_CHECK}
${ENV_CONFIG_DIR}"
    fi

    FOUND_CONFIG=0
    while IFS= read -r cfg; do
        if [ -n "${cfg}" ] && { [ -e "${cfg}" ] || [ -L "${cfg}" ]; }; then
            FOUND_CONFIG=1
            remove_path "${cfg}" "配置"
        fi
    done <<< "${CONFIG_TO_CHECK}"
    if [ "${FOUND_CONFIG}" -eq 0 ]; then
        note "没有找到配置残留"
    fi

    if [ "${DRY_RUN}" -eq 0 ]; then
        # 清掉偏好设置缓存（cfprefsd 可能还持有旧值）
        defaults delete "${BUNDLE_ID}" >/dev/null 2>&1 || true
    fi
else
    say "[4/4] 配置保留（未传 --purge）"
    if [ -d "${APP_SUPPORT_DIR}" ]; then
        note "配置目录：${APP_SUPPORT_DIR}"
    fi
    if [ -n "${ENV_CONFIG_DIR}" ] && [ -d "${ENV_CONFIG_DIR}" ]; then
        note "配置目录（ULINK_CONFIG_DIR）：${ENV_CONFIG_DIR}"
    fi
    if [ -d "${LOG_DIR}" ]; then
        note "日志目录：${LOG_DIR}"
    fi
    if [ ! -d "${APP_SUPPORT_DIR}" ] && [ ! -d "${LOG_DIR}" ]; then
        note "没有发现配置/日志目录"
    fi
    note "需要彻底清干净：./scripts/uninstall-link-mac.sh --purge"
fi

# ---- 收尾 ------------------------------------------------------------------
say ""
if [ "${FAILURES}" -gt 0 ]; then
    say "=============================================="
    say "  卸载未完成：${FAILURES} 项删除失败（见上面的警告）"
    say "=============================================="
    exit 1
fi

say "=============================================="
say "  卸载完成"
say "=============================================="
if [ "${DRY_RUN}" -eq 1 ]; then
    say "  （dry-run：以上动作都没有真正执行）"
else
    if pgrep -f "U-Link.app/Contents/MacOS/${BIN_NAME}" >/dev/null 2>&1; then
        say "  [!!] 仍检测到 U-Link 进程，请手动在菜单栏退出"
    fi
    if [ -f "${USER_LAUNCH_AGENT}" ]; then
        say "  [!!] LaunchAgent 仍存在：${USER_LAUNCH_AGENT}"
    fi
    if [ "${PURGE}" -eq 1 ]; then
        say "  程序 + 配置 已全部清除"
    else
        say "  程序已卸载；配置保留（--purge 可一并删除）"
    fi
fi
say ""
say "验收（docs/DESKTOP-APP.md §10）：卸载后应无残留自启项。"
say "  检查：launchctl print gui/${UID_NUM}/${LAUNCH_AGENT_LABEL} 应报 not found"
say "        ls ~/Library/LaunchAgents | grep u-link 应无输出"
