# IPMonitor

基于 [MyGo](https://github.com/egoist/mygo) 的 Go native UI 桌面程序，支持 Linux 和 Windows。它监听剪贴板变化，只提取开头的公网 IPv4/IPv6，通过 IPinfo Lite 显示国家 / 地区与 AS 名称。

## 功能

- Linux/Windows 原生 GPU UI，无 HTML、JavaScript、WebView 或 CGO。
- 托盘弹窗跟随任务栏 / 托盘图标所在显示器；Linux AppIndicator 没有图标坐标时贴近真实 X11 面板或显示器工作区边缘。
- 弹窗基准为 380×144 DIP，主信息 23、详情 17 DIP；按实际窗口空间自适应，系统 DPI 负责物理像素缩放。长 IP 按测量宽度调整字号，长国家/AS 文本换行或省略；小工作区会限制窗口尺寸。
- 历史列表与弹窗使用原版 19 个国家/地区的离线国旗绘制，未知代码显示蓝色 ISO 徽标，不使用 emoji 或网络图片。
- 托盘运行；窗口支持手动查询、历史记录、清空历史和中英文设置。
- 主窗口关闭只隐藏到托盘，再次打开直接前置；只有“退出”会销毁窗口。首次打开居中，之后恢复保存的位置和大小。
- 剪贴板使用系统变化事件，不轮询、不读取启动前的旧内容，只读取短文本。
- 在 Ubuntu Wayland 会话中，如果系统提供 XWayland，程序自动使用 XWayland 的 GTK clipboard owner-change 监听；纯 Wayland 且没有 XWayland 时会显示不可用，而不会轮询剪贴板。
- 只接受直接 IP、`http://IP` 或 `https://IP`；IPv6 端口使用方括号。
- 时间戳只精确匹配整个剪贴板：`17` 开头的 13 位 ASCII 数字，按 Unix 毫秒转换为系统本地时间的 `yyyy-mm-dd hh:mm:ss`（UTC+8 下 `1791880455533` → `2026-10-13 16:34:15`）。不接受空白、URL 或其他前后缀。
- 时间戳复用 IP 弹窗、定位和 5 秒自动隐藏；已过期红色、未过期绿色，显示期间到期会变红。不需 Token、不发请求、不加入历史或缓存，也不改变手动查询框。
- 私网、回环、链路本地、共享地址、文档地址、组播和保留地址在本地过滤，不发请求。
- 同一 IP 的成功结果缓存 1 小时，历史最多 100 条，退出即清空。
- 网络请求不自动重试，使用系统 HTTPS 校验，响应上限 64 KiB。

## 开发

要求 Go 1.27.1+。native UI 项目只需要 Go：

```sh
go test ./...
go run .
```

Linux 运行 native UI 需要 GTK 3；Ubuntu Wayland 后台全局剪贴板监听还需要 XWayland，托盘图标通常还需要 `libayatana-appindicator3`。Windows 10/11 不需要 WebView2。

真实桌面回归（Linux / Windows，在桌面会话中运行）：

```sh
IPMONITOR_NATIVE_TEST=1 go test -run '^TestNative' -v .
```

原生回归使用独立用户配置目录，覆盖首次居中、保存位置恢复、损坏状态文件和 20 次关闭重开，以及实际剪贴板时间戳转换、红绿显示、到期变色、历史/缓存不变和切回 IP 弹窗。GitHub Actions 在 Xvfb + Openbox 的 1280×720（1×）和 3840×2160（2×）环境中运行，并保存 IP、IPv6、中文和时间弹窗截图。

## 构建

使用标准 `go build`，不调用 MyGo CLI。Ubuntu 上生成发布文件需要 Go 1.27.1+、`dpkg-deb`、`xz` 及常规 GNU 工具；Windows 图标、版本信息和 PerMonitorV2 DPI 清单由固定版本的 Go 资源工具生成，无需 C 编译器或本机 Windows 环境：

```sh
scripts/build-release.sh 1.0.0
```

`build/release/` 仅生成 x64 的 `ipmonitor_1.0.0_amd64.deb`、`ipmonitor-linux-amd64` 和 `ipmonitor-windows-amd64.exe`。单文件不套 ZIP；Linux 裸程序下载后执行 `chmod +x ipmonitor-linux-amd64`，运行仍需 GTK 3 等系统库。

构建使用 `CGO_ENABLED=0`、`GOAMD64=v1`、`-trimpath`、`-s -w -buildid=`、关闭 UI 开发检查器及 `-gcflags=all=-l`（关闭内联减小体积，代价是增加函数调用开销）；Debian 包使用 xz 压缩。不使用 UPX。

GitHub Actions 在 Pull Request 和 `main` 推送上测试和构建；推送 `v*` 标签后，自动发布 [GitHub Release](https://github.com/Factory48/ipmonitor/releases)，附件仅包含上述三个文件。工作流不包含代码签名。

## 许可证

本项目按 MIT 许可证发布；MyGo 及其依赖按各自许可证发布。
