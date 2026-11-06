#!/usr/bin/env bash
# ============================================================
# package-mac-link.sh — 把 build/u-link 打包成 macOS 菜单栏 .app
#
# 用法：
#   ./scripts/package-mac-link.sh                # 版本号取 git describe
#   VERSION=v0.6.0 ./scripts/package-mac-link.sh # 环境变量覆盖
#   ./scripts/package-mac-link.sh v0.6.0         # 位置参数覆盖（优先级最高）
#   ./scripts/package-mac-link.sh --help
#
# 产物：
#   build/U-Link.app                菜单栏常驻应用（LSUIElement=1，无 Dock 图标）
#   build/U-Link-mac-<arch>.zip     ditto 打包，可直接分发
#
# 前置：build/u-link 已经构建好（macOS 需要 cgo）。
#   本脚本**不编译** Go 代码，构建见 docs/DESKTOP-APP.md
#   （make link-package-mac 或等价的 CGO_ENABLED=1 go build）。
#
# 失败语义：任何一步失败都以非 0 退出，且不留下半成品——
#   .app 先在 build/.u-link-stage.<pid>/ 里组装，成功后才原子换入
#   build/U-Link.app；zip 同样先写临时文件再改名。
#   已存在的旧版 build/U-Link.app / zip 在本次失败时原样保留。
# ============================================================
set -euo pipefail

# 一切路径以仓库根为基准（脚本可能在任意 cwd 被调用）
cd "$(dirname "$0")/.."
REPO_ROOT="$(pwd)"

BUILD_DIR="build"
BIN="${BUILD_DIR}/u-link"
APP="${BUILD_DIR}/U-Link.app"
STAGE_ROOT="${BUILD_DIR}/.u-link-stage.$$"
STAGE_APP="${STAGE_ROOT}/U-Link.app"

BUNDLE_ID="org.uclaw.u-link"
EXEC_NAME="u-link"

# 版本号优先级：命令行参数 > 环境变量 VERSION > git describe > dev
#   git describe 形如 v1.0.0-26-g4674c3c-dirty：完整串写进 ULinkVersion，
#   plist 的 CFBundleShortVersionString 取其中的数字部分 1.0.0。
DEFAULT_VERSION="$(git describe --tags --always --dirty 2>/dev/null || echo dev)"
DEFAULT_VERSION="${DEFAULT_VERSION:-dev}"
VERSION="${1:-${VERSION:-${DEFAULT_VERSION}}}"

# 供 cleanup 使用的状态（必须在 trap 之前初始化，set -u 才不会炸）
ZIP=""
ZIP_TMP=""
ZIP_FINAL=0
SUCCESS=0

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
用法: ./scripts/package-mac-link.sh [版本号]

把 build/u-link 打包成菜单栏应用：
  build/U-Link.app
  build/U-Link-mac-<arch>.zip

版本号来源（优先级从高到低）：
  1. 命令行第一个参数，例如 v0.6.0
  2. 环境变量 VERSION，例如 VERSION=v0.6.0 ./scripts/package-mac-link.sh
  3. git describe --tags --always --dirty
  4. dev

CFBundleShortVersionString 只接受数字点分版本（最多三段），因此会把
v1.0.0-26-g4674c3c-dirty 这类串截成 1.0.0；完整原始值写入自定义键
ULinkVersion，方便排查。

退出码：
  0  成功
  1  失败（二进制缺失 / 工具缺失 / 签名或打包失败），半成品会被清理
USAGE
}

# ---- 失败清理 --------------------------------------------------------------
# 只在本次没有成功时动手：删掉暂存目录与未完成的 zip；
# 已存在的 build/U-Link.app 不在 cleanup 范围里（换入是在成功路径上做的）。
cleanup() {
    rc=$?
    if [ "${SUCCESS}" -eq 1 ]; then
        return 0
    fi
    if [ -n "${STAGE_ROOT}" ] && [ -d "${STAGE_ROOT}" ]; then
        rm -rf "${STAGE_ROOT}" 2>/dev/null || true
    fi
    if [ -n "${ZIP_TMP}" ] && [ -f "${ZIP_TMP}" ]; then
        rm -f "${ZIP_TMP}" 2>/dev/null || true
    fi
    if [ -n "${ZIP}" ] && [ -f "${ZIP}" ] && [ "${ZIP_FINAL}" -ne 1 ]; then
        rm -f "${ZIP}" 2>/dev/null || true
    fi
    if [ "${rc}" -ne 0 ]; then
        printf '\n[FAIL] 打包失败（退出码 %s），临时产物已清理，build/ 保持原状。\n' "${rc}" >&2
    fi
    return 0
}
trap cleanup EXIT

# ---- 参数 ------------------------------------------------------------------
if [ "$#" -gt 0 ]; then
    case "$1" in
        -h|--help)
            usage
            exit 0
            ;;
        -*)
            die "未知参数：$1（用 --help 查看用法）"
            ;;
    esac
fi

say "=============================================="
say "  U-Link · macOS 打包"
say "=============================================="

# ---- 环境检查 --------------------------------------------------------------
[ "$(uname -s)" = "Darwin" ] || die "本脚本只能在 macOS 上运行（当前系统：$(uname -s)），请到 mac 机器或 CI 的 mac runner 上执行。"

for tool in ditto codesign plutil; do
    command -v "${tool}" >/dev/null 2>&1 || {
        if [ "${tool}" = "codesign" ]; then
            die "找不到 ${tool}，请先安装 Xcode Command Line Tools：xcode-select --install"
        fi
        die "找不到系统自带的 ${tool}，请检查 PATH（/usr/bin 是否被破坏？）"
    }
done

# ---- 二进制检查（缺了就清晰报错退出 1） -------------------------------------
[ -f "${BIN}" ] || die "找不到 macOS 二进制 ${REPO_ROOT}/${BIN}。请先构建它：
  CGO_ENABLED=1 go build -trimpath -o build/u-link ./cmd/link
（或运行 make link-package-mac 让 Makefile 先编译再打包，见 docs/DESKTOP-APP.md）"

[ -s "${BIN}" ] || die "${BIN} 是空文件，请重新构建后再打包。"

[ -x "${BIN}" ] || die "${BIN} 不可执行。请先 chmod +x \"${BIN}\"，或重新构建。"

if command -v file >/dev/null 2>&1; then
    if ! file "${BIN}" | grep -q 'Mach-O'; then
        die "${BIN} 不是 Mach-O 可执行文件（是不是误拿了 Windows/Linux 的产物？）"
    fi
    note "二进制：$(file -b "${BIN}" | head -n1)"
fi

# ---- 架构检测（决定 zip 文件名） --------------------------------------------
ARCH=""
if command -v lipo >/dev/null 2>&1; then
    LIPO_ARCHS="$(lipo -archs "${BIN}" 2>/dev/null || true)"
    case "${LIPO_ARCHS}" in
        "")
            ARCH=""
            ;;
        *" "*)
            # 多个架构（universal）。取主架构做标签没有意义，直接标 universal。
            ARCH="universal"
            ;;
        arm64|arm64e)
            ARCH="arm64"
            ;;
        x86_64)
            ARCH="x86_64"
            ;;
        *)
            ARCH="${LIPO_ARCHS}"
            ;;
    esac
fi
if [ -z "${ARCH}" ]; then
    case "$(uname -m)" in
        arm64) ARCH="arm64" ;;
        x86_64) ARCH="x86_64" ;;
        *) ARCH="$(uname -m)" ;;
    esac
    warn "无法用 lipo 判断二进制架构，按本机架构处理：${ARCH}"
fi

ZIP="${BUILD_DIR}/U-Link-mac-${ARCH}.zip"
ZIP_TMP="${ZIP}.part.$$"

# ---- 版本号 ----------------------------------------------------------------
# CFBundleShortVersionString 要求“最多三段的非负整数”，所以：
#   从 VERSION 里取出数字点分部分（v1.0.0-26-g4674c3c-dirty → 1.0.0）；
#   取不出来（如 dev）→ 回落 0.0.0，原始值仍然写进 ULinkVersion。
NUM_VERSION="${VERSION#[vV]}"
# 截到第一个 '-'（git describe 的提交数/哈希）或 '+'（build metadata）之前
BASE_VERSION="${NUM_VERSION%%-*}"
BASE_VERSION="${BASE_VERSION%%+*}"
if printf '%s' "${BASE_VERSION}" | grep -Eq '^[0-9]+(\.[0-9]+){0,2}$'; then
    SHORT_VERSION="${BASE_VERSION}"
else
    SHORT_VERSION="0.0.0"
    warn "VERSION=${VERSION} 里没有可用的数字版本号，CFBundleShortVersionString 回落为 0.0.0（原始值仍写入 ULinkVersion）。正式发布建议 VERSION=v0.6.0。"
fi
CF_BUNDLE_VERSION="${SHORT_VERSION}"
# 原始版本串清洗成只含安全字符，避免写入 plist 时破坏 XML
SAFE_VERSION="$(printf '%s' "${VERSION}" | sed 's/[^A-Za-z0-9._-]/_/g')"

say ""
note "版本：${VERSION}（plist: CFBundleShortVersionString=${SHORT_VERSION}, CFBundleVersion=${CF_BUNDLE_VERSION}）"
note "架构：${ARCH}"

# ---- 可选图标 --------------------------------------------------------------
# 菜单栏应用不强制要图标；如果仓库里放了 .icns 就顺手带上。
ICON_SRC=""
for cand in "${BUILD_DIR}/u-link.icns" "assets/u-link.icns" "scripts/u-link.icns"; do
    if [ -f "${cand}" ]; then
        ICON_SRC="${cand}"
        break
    fi
done

# ---- 在暂存目录里组装 .app -------------------------------------------------
say ""
say "[1/5] 组装 .app 目录结构"
rm -rf "${STAGE_ROOT}"
mkdir -p "${STAGE_APP}/Contents/MacOS" "${STAGE_APP}/Contents/Resources"

cp -p "${BIN}" "${STAGE_APP}/Contents/MacOS/${EXEC_NAME}"
chmod 755 "${STAGE_APP}/Contents/MacOS/${EXEC_NAME}"
ok "Contents/MacOS/${EXEC_NAME}"

if [ -n "${ICON_SRC}" ]; then
    cp -p "${ICON_SRC}" "${STAGE_APP}/Contents/Resources/u-link.icns"
fi

cat > "${STAGE_APP}/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleDevelopmentRegion</key>
	<string>zh_CN</string>
	<key>CFBundleExecutable</key>
	<string>${EXEC_NAME}</string>
	<key>CFBundleIdentifier</key>
	<string>${BUNDLE_ID}</string>
	<key>CFBundleInfoDictionaryVersion</key>
	<string>6.0</string>
	<key>CFBundleName</key>
	<string>U-Link</string>
	<key>CFBundleDisplayName</key>
	<string>U-Link</string>
	<key>CFBundlePackageType</key>
	<string>APPL</string>
	<key>CFBundleShortVersionString</key>
	<string>${SHORT_VERSION}</string>
	<key>CFBundleVersion</key>
	<string>${CF_BUNDLE_VERSION}</string>
	<key>CFBundleSignature</key>
	<string>????</string>
	<!-- LSUIElement=1：只在菜单栏出现，不占 Dock、不出现在 Cmd-Tab -->
	<key>LSUIElement</key>
	<true/>
	<!-- 菜单栏应用天然单实例，避免双击两次起两个托盘图标 -->
	<key>LSMultipleInstancesProhibited</key>
	<true/>
	<key>NSHighResolutionCapable</key>
	<true/>
	<key>LSMinimumSystemVersion</key>
	<string>11.0</string>
	<!-- 原始构建版本（可能是 dev / 带 v 前缀），便于排查 -->
	<key>ULinkVersion</key>
	<string>${SAFE_VERSION}</string>
</dict>
</plist>
PLIST
ok "Contents/Info.plist"

if [ -n "${ICON_SRC}" ]; then
    if [ -x /usr/libexec/PlistBuddy ]; then
        /usr/libexec/PlistBuddy -c "Add :CFBundleIconFile string u-link" \
            "${STAGE_APP}/Contents/Info.plist" >/dev/null
        ok "图标：${ICON_SRC}（已写入 CFBundleIconFile）"
    else
        warn "找到图标 ${ICON_SRC} 但缺少 PlistBuddy，已跳过图标引用"
    fi
fi

# ---- 校验 Info.plist -------------------------------------------------------
say ""
say "[2/5] 校验 Info.plist"
PLIST_PATH="${STAGE_APP}/Contents/Info.plist"
PLUTIL_OUT="$(plutil -lint "${PLIST_PATH}" 2>&1)" || die "Info.plist 语法错误：${PLUTIL_OUT}"
ok "${PLUTIL_OUT}"

for key in CFBundleExecutable CFBundleIdentifier CFBundleName \
           CFBundleShortVersionString CFBundleVersion \
           LSUIElement NSHighResolutionCapable; do
    grep -q "<key>${key}</key>" "${PLIST_PATH}" || die "Info.plist 缺少关键字段 ${key}"
done

plist_val() {
    plutil -extract "$1" raw "${PLIST_PATH}" 2>/dev/null || true
}
[ "$(plist_val CFBundleExecutable)" = "${EXEC_NAME}" ] \
    || die "CFBundleExecutable 应为 ${EXEC_NAME}，实际为 '$(plist_val CFBundleExecutable)'"
[ "$(plist_val CFBundleIdentifier)" = "${BUNDLE_ID}" ] \
    || die "CFBundleIdentifier 应为 ${BUNDLE_ID}，实际为 '$(plist_val CFBundleIdentifier)'"
[ "$(plist_val CFBundleName)" = "U-Link" ] \
    || die "CFBundleName 应为 U-Link，实际为 '$(plist_val CFBundleName)'"
[ "$(plist_val LSUIElement)" = "true" ] \
    || die "LSUIElement 必须是 true（菜单栏常驻、不占 Dock），实际为 '$(plist_val LSUIElement)'"
[ "$(plist_val NSHighResolutionCapable)" = "true" ] \
    || die "NSHighResolutionCapable 必须是 true，实际为 '$(plist_val NSHighResolutionCapable)'"
[ "$(plist_val CFBundleShortVersionString)" = "${SHORT_VERSION}" ] \
    || die "CFBundleShortVersionString 与预期不符"
ok "关键字段全部就位（${BUNDLE_ID} · LSUIElement=1 · 版本 ${SHORT_VERSION}）"

# ---- ad-hoc 签名 -----------------------------------------------------------
say ""
say "[3/5] ad-hoc 签名（codesign --force --sign -）"
# 去掉 xattr（资源分支/Finder 信息会让 codesign 直接失败）
xattr -cr "${STAGE_APP}" 2>/dev/null || true
codesign --force --sign - "${STAGE_APP}" >/dev/null 2>&1 \
    || die "codesign 失败。可手动复现：codesign --force --sign - \"${STAGE_APP}\""

if ! codesign --verify --strict "${STAGE_APP}" >/dev/null 2>&1; then
    die "ad-hoc 签名校验失败：codesign --verify --strict \"${STAGE_APP}\""
fi
SIGNED_ID="$(codesign -dv "${STAGE_APP}" 2>&1 | sed -n 's/^Identifier=//p' | head -n1 || true)"
if [ "${SIGNED_ID}" != "${BUNDLE_ID}" ]; then
    die "签名里的 Identifier 应为 ${BUNDLE_ID}，实际为 '${SIGNED_ID}'"
fi
ok "签名有效（Identifier=${SIGNED_ID}，ad-hoc / 未公证）"

# ---- ditto 打 zip（对暂存 app 打包，成功校验后才换入） ----------------------
say ""
say "[4/5] ditto 打包 zip"
rm -f "${ZIP_TMP}"
# 用暂存目录里的 app 打包；--keepParent 保证压缩包根节点就是 U-Link.app。
# --norsrc：不保留资源分支 / 扩展属性——本应用用不到，签名在 Mach-O 内和
# _CodeSignature/CodeResources 里；不加的话 macOS 会往 zip 里塞一堆
# __MACOSX/._xxx 的 AppleDouble 噪音文件。
ditto -c -k --norsrc --keepParent "${STAGE_APP}" "${ZIP_TMP}" \
    || die "ditto 打包失败"
[ -s "${ZIP_TMP}" ] || die "ditto 生成的 zip 为空"

# 注意：这里不能写 `unzip -l "${ZIP}" | grep -q ...`——grep -q 命中后立刻退出，
# unzip 吃到 SIGPIPE，配合 set -o pipefail 会把「成功」判成失败。
# 先把清单收进变量再用 case 匹配。
if command -v unzip >/dev/null 2>&1; then
    ZIP_LISTING="$(unzip -l "${ZIP_TMP}" 2>/dev/null || true)"
    case "${ZIP_LISTING}" in
        *"U-Link.app/Contents/Info.plist"*) ;;
        *) die "zip 结构异常：里面没有 U-Link.app/Contents/Info.plist" ;;
    esac
    case "${ZIP_LISTING}" in
        *"U-Link.app/Contents/MacOS/${EXEC_NAME}"*) ;;
        *) die "zip 结构异常：里面没有 U-Link.app/Contents/MacOS/${EXEC_NAME}" ;;
    esac
    ok "zip 结构校验通过（含 Info.plist 与 MacOS/${EXEC_NAME}）"
else
    warn "找不到 unzip，跳过 zip 结构校验"
fi

# ---- 换入 build/U-Link.app 与 zip ------------------------------------------
say ""
say "[5/5] 安装到 build/"

# 换入前先确认目标合法（防止误删）
case "${APP}" in
    "${BUILD_DIR}"/U-Link.app) ;;
    *) die "内部错误：目标路径异常（${APP}）" ;;
esac

if [ -e "${APP}" ] || [ -L "${APP}" ]; then
    note "覆盖已有 ${APP}"
    rm -rf "${APP}"
fi
mv "${STAGE_APP}" "${APP}"
ok "${REPO_ROOT}/${APP}"

mv -f "${ZIP_TMP}" "${ZIP}"
ZIP_FINAL=1
ok "${REPO_ROOT}/${ZIP}"

rm -rf "${STAGE_ROOT}"

# ---- 完成 ------------------------------------------------------------------
SUCCESS=1

APP_SIZE="$(du -sh "${APP}" | awk '{print $1}')"
ZIP_SIZE="$(du -sh "${ZIP}" | awk '{print $1}')"

say ""
say "=============================================="
say "  打包完成"
say "=============================================="
say "  .app : ${REPO_ROOT}/${APP}  (${APP_SIZE})"
say "  .zip : ${REPO_ROOT}/${ZIP}  (${ZIP_SIZE})"
say "  版本 : ${VERSION}（plist 里是 ${SHORT_VERSION}）    架构：${ARCH}"
say ""
say "下一步："
say "  • 直接启动（菜单栏出现状态点）："
say "      open \"${APP}\""
say "  • 装到 /Applications："
say "      ditto \"${APP}\" /Applications/U-Link.app"
say "  • 分发：把 ${ZIP} 发出去；对方解压后若被 Gatekeeper 拦住，执行"
say "      xattr -dr com.apple.quarantine /Applications/U-Link.app"
say "  • 卸载：./scripts/uninstall-link-mac.sh"
say "  • 详细说明（首次打开 / 配置 token / 常见问题）：docs/DESKTOP-APP.md"
