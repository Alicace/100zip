<div align="center">

<img src="ICON_256.PNG" alt="100zip" width="128" />

# 100zip

**面向飞牛 fnOS NAS 的原生压缩与解压工具**

在 NAS 本机完成压缩、解压、预览和批量任务，无需 Docker，不上传文件到第三方服务。

[![Release](https://img.shields.io/github/v/release/Alicace/100zip?display_name=tag)](https://github.com/Alicace/100zip/releases)
[![Downloads](https://img.shields.io/github/downloads/Alicace/100zip/total)](https://github.com/Alicace/100zip/releases)
![fnOS](https://img.shields.io/badge/fnOS-x86__64-2ea44f)
[![License](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

[下载 Releases](https://github.com/Alicace/100zip/releases) · [问题反馈](https://github.com/Alicace/100zip/issues)

</div>

---

## 功能概览

| 模块 | 支持内容 |
| --- | --- |
| 解压 | 7z、ZIP、RAR、TAR、GZ、BZ2、XZ、ZST、ISO、CAB、WIM、VHDX 等常见格式 |
| 多卷压缩包 | `.7z.001`、`.zip.001`、`.z01`、`.part1.rar`、`.r00` 等，自动检测缺卷 |
| 压缩 | 创建 7z、ZIP、TAR、tar.gz、BZ2、XZ，支持分卷、密码和自定义文件名 |
| 批量处理 | 批量解压到相互独立的目录，批量创建多份压缩包 |
| 包内预览 | 图片、视频、音频、文本、PDF、Word、Excel、PowerPoint 等常用文件 |
| 文件名与密码 | 中文文件名编码修复、密码输入、密码库和按需显示密码 |
| 任务中心 | 队列、并发控制、进度、取消、失败详情和诊断日志 |
| 移动端 | 针对手机页面优化的任务列表、预览窗口和操作栏 |

## 支持平台

- 飞牛 fnOS x86_64（当前提供 `x86_64` FPK）
- Go 后端已静态编译进安装包，NAS 不需要额外安装 Node.js 或 Python
- 大文件和几十 GB、上百 GB 任务会根据可用磁盘空间、CPU 并发和文件系统能力执行；开始前会检查空间，任务中显示进度与错误原因

## 安装

1. 从 [Releases](https://github.com/Alicace/100zip/releases) 下载对应的 `.fpk` 文件。
2. 在 fnOS「应用中心 → 手动安装」中选择 FPK。
3. 安装后从桌面打开 **100zip**。

也可以通过 NAS 终端安装：

```bash
appcenter-cli stop 100zip || true
appcenter-cli uninstall 100zip || true
appcenter-cli install-fpk /tmp/100zip_0.5.17_x86_64.fpk
appcenter-cli start 100zip
```

## 校验安装包

```text
文件：100zip_0.5.17_x86_64.fpk
SHA256：C72F76F1BAFFC651B0E00244043068CAA6AE5CFB34CED218678BEACCFA902B58
```

## 使用说明

- 首次使用时，请在应用内授权需要访问的共享目录。
- 加密压缩包会在需要时提示输入密码，也可以从密码库选择已保存密码。
- 批量解压会为不同压缩包创建独立目录，避免文件互相覆盖。
- 压缩和解压速度主要取决于 NAS CPU、磁盘读写速度、压缩格式和压缩等级。
- RAR 支持解压，不提供 RAR 创建；7-Zip 组件遵循其随附许可证。

## 项目状态

当前版本：**v0.5.17**

项目持续修复预览、移动端布局、任务交互和大文件处理体验。欢迎通过 [Issues](https://github.com/Alicace/100zip/issues) 提交问题或建议。

## 许可证

本项目代码采用 [MIT License](LICENSE)。随安装包分发的 7-Zip 引擎按其随附许可证使用。本项目与飞牛官方无隶属关系。
