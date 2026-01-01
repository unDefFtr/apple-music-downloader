# Apple Music 下载器

一个功能强大的命令行工具，用于下载 Apple Music 的歌曲、专辑、播放列表、电台和音乐视频 (MV)，支持 **ALAC** (无损/高解析度)、**Dolby Atmos** (杜比全景声) 和 **AAC** 格式。

[English](./README.md) | 简体中文

## ✨ 功能特性

- **高音质支持**: 支持 ALAC (无损 / Hi-Res), Dolby Atmos (E-AC-3), 和 AAC (256kbps).
- **多媒体支持**: 支持下载歌曲、专辑、播放列表、电台、歌手所有专辑以及 MV (最高 4K).
- **交互式搜索**: 支持在命令行中直接搜索专辑、歌曲或歌手.
- **元数据完善**: 自动添加封面、元数据标签、歌词 (支持同步、逐字和翻译歌词).
- **额外功能**: 支持下载动态封面 (需要 ffmpeg) 和格式转换.

## 🛠️ 前置要求

在使用本工具之前，请确保已安装以下软件：

1.  **Go**: 用于运行下载器.
2.  **[MP4Box (GPAC)](https://gpac.io/downloads/gpac-nightly-builds/)**: **必须安装**，用于封装和添加标签。请确保将其添加到系统环境变量中.
3.  **[mp4decrypt (Bento4)](https://www.bento4.com/downloads/)**: **可选** (仅下载 MV 时需要，下载歌曲/专辑不需要).
4.  **[ffmpeg](https://ffmpeg.org/download.html)**: **可选** (用于格式转换和动态封面处理).
5.  **解密 Wrapper**: 下载 ALAC 和 Dolby Atmos 需要运行解密 Wrapper 服务 (例如 [wrapper](https://github.com/WorldObservationLog/wrapper)).

## 🚀 安装与配置

1.  **克隆仓库**:
    ```bash
    git clone https://github.com/unDefFtr/apple-music-downloader.git
    cd apple-music-downloader
    ```

2.  **配置文件**:
    复制示例配置文件并进行修改:
    ```bash
    cp config.example.yaml config.yaml
    ```
    *   **media-user-token**: 下载 AAC-LC、MV 和歌词所必须。获取方法见下文 "如何获取 media-user-token".
    *   **decrypt-m3u8-port**: 设置为解密 Wrapper 的地址 (如果本地运行，通常默认即可).

## 📖 使用方法

### 基本用法

直接运行并附带链接:

```bash
go run main.go [链接]
```

### 搜索模式 (交互式)

交互式搜索媒体资源:

```bash
go run main.go --search [album|song|artist] "搜索关键词"
```
*示例*: `go run main.go --search artist "Taylor Swift"`

### 命令行参数

| 参数 | 说明 |
| :--- | :--- |
| `--search [type]` | 搜索模式 (`album` 专辑, `song` 歌曲, `artist` 歌手). |
| `--song` | 从专辑链接中下载单首歌曲. |
| `--select` | 交互式选择专辑/播放列表中的歌曲进行下载. |
| `--all-album` | 当提供歌手链接时，下载该歌手的所有专辑. |
| `--atmos` | 下载 **Dolby Atmos** (杜比全景声) 格式. |
| `--aac` | 下载 **AAC** 格式. |
| `--debug` | 开启调试模式 (显示音质详情). |
| `--thread [num]` | 设置下载线程数 (默认: 1). |

### 使用示例

**下载专辑:**
```bash
go run main.go https://music.apple.com/us/album/1989-taylors-version/1708308989
```

**下载单曲:**
```bash
go run main.go --song https://music.apple.com/us/album/cruel-summer/1468027721?i=1468027728
```

**选择特定曲目下载:**
```bash
go run main.go --select https://music.apple.com/us/album/midnights/1649438810
```

**下载杜比全景声 (Dolby Atmos):**
```bash
go run main.go --atmos https://music.apple.com/us/album/1989-taylors-version/1708308989
```

**下载音乐视频 (MV):**
```bash
go run main.go https://music.apple.com/us/music-video/anti-hero/1649439395
```

## 🐳 Docker 使用方法

1.  **构建镜像**:
    ```bash
    docker build -t apple-music-dl .
    ```

2.  **运行**:
    确保已配置好 `config.yaml` 且 Wrapper 服务可访问.
    ```bash
    docker run --network host -v $(pwd)/downloads:/downloads apple-music-dl [链接/参数]
    ```

## 🔑 如何获取 `media-user-token`

1.  在浏览器中打开 [Apple Music](https://music.apple.com) 并登录.
2.  打开开发者工具 (F12) -> Application (应用程序) 或 Storage (存储) -> Cookies.
3.  找到 `https://music.apple.com` 域下的 Cookies，找到名为 `media-user-token` 的项.
4.  复制其值并粘贴到 `config.yaml` 文件中.

## 📝 注意事项

- **MP4Box** 是必须的。如果遇到 "tagging" 或 "packaging" 相关的错误，请检查 MP4Box 是否正确安装并配置了环境变量.
- **ALAC/Atmos** 下载需要配合解密 Wrapper 使用.
- **歌词** 和 **MV** 下载需要有效的 `media-user-token` (且账号需有订阅).

## 📜 免责声明

本工具仅供学习和教育目的使用。请支持艺术家，在官方平台流媒体播放或购买音乐。
