# 100zip（100解压）

面向飞牛 fnOS NAS 的本地压缩与解压工具。应用以原生 FPK 安装，不依赖 Docker；文件处理全部在 NAS 本机完成，适合家庭 NAS、媒体库和日常文件归档场景。

当前版本：**v0.5.17**（x86_64）

## 项目特点

- 原生 FPK 应用，统一网关访问 `/app/100zip/`
- Go 常驻服务 + 原生 HTML/CSS/JavaScript 界面
- 内置 7-Zip 静态引擎，支持大文件、分卷和密码包
- 路径访问经过授权根目录保护，不把文件上传到第三方服务
- 任务队列支持并发、取消、失败信息、密码等待和历史记录
- 手机端响应式布局，适配任务列表、预览和操作栏

## 支持的功能

### 解压

- 支持 7z、ZIP、RAR、TAR、GZ、BZ2、XZ、ZST、ISO、CAB、WIM、VHDX 等常见格式
- 自动识别 `.7z.001`、`.zip.001`、`.z01`、`.part1.rar`、`.r00` 等分卷
- 缺卷检测和明确提示，避免把分卷不完整误报为普通损坏
- 支持选择部分条目解压
- 支持同名子目录、自动改名、覆盖/跳过策略
- 支持中文文件名编码检测和修复
- 支持密码输入、密码库和密码库自动尝试

### 压缩

- 创建 7z、ZIP、TAR、tar.gz、tar.bz2、tar.xz 等格式
- 文件名可自定义，扩展名会随格式自动切换
- 支持自定义分卷大小和常用预设（MB/GB/TB）
- 支持压缩率、压缩方法、字典大小和 7z 固实压缩
- 支持密码、密码二次确认和密码显示/隐藏
- 支持完成后完整性测试
- 可选在压缩成功并测试通过后删除源文件
- 支持多个来源合并压缩，或每个来源独立输出一个压缩包

### 批量处理

- 扫描目录中的压缩包并批量解压
- 自动跳过分卷后续卷，避免重复任务
- 同名不同格式压缩包自动隔离到不同子目录
- 支持自定义目标目录、并发数和密码库尝试
- 大任务提交前估算空间，不足时阻止提交并给出提示

### 包内预览

- 图片：PNG、JPG、GIF、WEBP、BMP、SVG 等
- 文本：TXT、Markdown、JSON、XML、CSV、代码和日志
- 音频/视频：MP3、WAV、FLAC、M4A、MP4、WebM 等（取决于浏览器编码支持）
- PDF：内嵌预览、页面宽度适配、全屏预览
- DOCX/XLSX/PPTX：文字内容预览
- 图片、PDF、文本和 Office 文件均支持点击预览，双击可放大；预览窗口支持全屏

> DOCX/XLSX/PPTX 当前是内容级预览，不承诺完全还原 Word 字体、分页、表格、图片和幻灯片版式。完整排版还原需要额外部署 LibreOffice 或 OnlyOffice 转换引擎。

### 任务与诊断

- 任务进度、速度、预计剩余时间和状态展示
- 任务取消、删除、等待密码和失败详情
- 诊断报告记录客户端环境、用户操作、任务参数、文件格式、容量估算和日志尾部
- 密码不会写入诊断报告

### 设置与更新

- 最大并发根据 NAS CPU 核心数生成建议
- 当前压缩/解压主要使用 CPU 和磁盘吞吐，不使用 GPU
- 密码库采用 AES-256-GCM 本地加密保存
- 设置页支持检查 GitHub Releases 最新版本

## 性能说明

速度主要取决于 NAS CPU、磁盘读写、网络盘吞吐、压缩格式、压缩率、固实压缩和加密开销。v0.5.17 已显式启用 7-Zip 多线程。机械硬盘建议并发 1～2，SSD/NVMe 可按 CPU 核心数提高并发。

## 构建

前置：Go 1.22+、fnpack、7-Zip Linux x64 官方归档。

```powershell
pwsh -File scripts/fetch-vendor.ps1
pwsh -File scripts/build-fpk.ps1 -Version 0.5.17
```

检查代码：

```bash
go vet ./...
go test ./...
```

产物位于 `dist/100zip_<版本>_x86_64.fpk`。

## 飞牛 NAS 安装

```bash
appcenter-cli stop 100zip || true
appcenter-cli uninstall 100zip || true
appcenter-cli install-fpk /path/to/100zip_0.5.17_x86_64.fpk
appcenter-cli start 100zip
```

## 更新发布方式

推荐使用 GitHub Releases：

1. GitHub 仓库保存源码、README 和变更记录。
2. 每次发布创建一个版本标签，例如 `v0.5.17`。
3. 将对应的 `.fpk` 文件上传到 Release 附件。
4. 应用设置页读取 Releases 元数据，提示是否有新版本。

不需要公开源码才能发布 FPK；仓库可以设置为 Private。但如果希望其他用户下载和更新，Release 及附件需要公开，或者提供可访问的下载地址。

## 目录结构

```text
manifest / config / cmd / wizard / app   # FPK 包内容
server/                                  # Go 服务入口
internal/{engine,jobs,paths,codepage,httpapi,apperr}
app/www/                                 # 前端页面
app/vendor/7zip/linux-x64/7zzs           # 7-Zip 引擎
scripts/                                 # 获取依赖和打包脚本
tools/                                   # FPK 权限修正与构建工具
docs/                                    # 设计、接口和运维文档
```

## 合规说明

- 本项目与飞牛官方无隶属关系。
- 7-Zip 组件按其随附许可证分发，仅提供 RAR 解压，不提供 RAR 创建。
- 本项目代码采用 MIT License，第三方组件以各自许可证为准。
