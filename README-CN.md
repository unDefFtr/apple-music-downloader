# Apple Music 下载器

一个功能强大的命令行工具，用于下载 Apple Music 的歌曲、专辑、播放列表、电台和音乐视频 (MV)，支持 **ALAC** (无损/高解析度)、**Dolby Atmos** (杜比全景声) 和 **AAC** 格式。

[English](./README.md) | 简体中文

## ✨ 功能特性

- **高音质支持**: 支持 ALAC (无损 / Hi-Res), Dolby Atmos (E-AC-3), 和 AAC (256kbps).
- **多媒体支持**: 支持下载歌曲、专辑、播放列表、电台、歌手所有专辑以及 MV (最高 4K).
- **搜索**: 支持在命令行中直接搜索专辑、歌曲、歌手或播放列表.
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
    *   **auth.media-user-token**: 下载 AAC-LC、MV 和歌词所必须。获取方法见下文 "如何获取 media-user-token".
    *   **download.m3u8.decrypt-port**: 设置为解密 Wrapper 的地址 (如果本地运行，通常默认即可).

## 📖 使用方法

### 基本用法

直接运行并附带链接:

```bash
go run main.go download [链接]
```

### 搜索模式

搜索媒体资源:

```bash
go run main.go search [album|song|artist|playlist] "搜索关键词"
```
*示例*: `go run main.go search artist "Taylor Swift"`

### 下载参数

| 参数 | 说明 |
| :--- | :--- |
| `--codec <aac|alac|atmos>` | 选择音频编码. |
| `--max-quality <value>` | 最高音质 (如 `192k`, `2768`). |
| `--lyrics` | 启用歌词下载. |
| `--lyrics-format <ttml|lrc>` | 歌词格式. |
| `--lyrics-type <plain|syllable>` | 歌词类型. |
| `--embed-lyrics` | 嵌入歌词到音频. |
| `--save-lyrics` | 另存歌词文件. |
| `--no-cover` | 禁用封面嵌入/保存. |
| `--cover-file` | 保存封面图片文件. |
| `--cover-name <name>` | 封面文件名(不含扩展名). |
| `--cover-size <size>` | 封面尺寸 (如 `5000`). |
| `--cover-format <jpg|png|original>` | 封面格式. |
| `--output <path>` | 输出目录. |
| `--threads <num>` | 下载线程数. |
| `--select` | 交互式选择曲目. |
| `--preset <default|lossless|archival|minimal>` | 预设模式. |
| `--convert <flac|mp3|opus|wav>` | 下载后转码. |
| `--keep-original` | 转码后保留原文件. |

### 使用示例

**下载专辑:**
```bash
go run main.go download https://music.apple.com/us/album/1989-taylors-version/1708308989
```

**下载单曲:**
```bash
go run main.go download song https://music.apple.com/us/song/cruel-summer/1468027728
```

**选择特定曲目下载:**
```bash
go run main.go download https://music.apple.com/us/album/midnights/1649438810 --select
```

**下载杜比全景声 (Dolby Atmos):**
```bash
go run main.go download https://music.apple.com/us/album/1989-taylors-version/1708308989 --codec atmos
```

**下载音乐视频 (MV):**
```bash
go run main.go download https://music.apple.com/us/music-video/anti-hero/1649439395
```

## 🐳 Docker 使用方法

1.  **构建镜像**:
    ```bash
    docker build -t apple-music-dl .
    ```

2.  **运行**:
    确保已配置好 `config.yaml` 且 Wrapper 服务可访问.
    ```bash
    docker run --network host -v $(pwd)/downloads:/downloads apple-music-dl download [链接/参数]
    ```

## 🔑 如何获取 `media-user-token`

1.  在浏览器中打开 [Apple Music](https://music.apple.com) 并登录.
2.  打开开发者工具 (F12) -> Application (应用程序) 或 Storage (存储) -> Cookies.
3.  找到 `https://music.apple.com` 域下的 Cookies，找到名为 `media-user-token` 的项.
4.  复制其值并粘贴到 `config.yaml` 的 `auth.media-user-token` 中.

## 📝 注意事项

- **MP4Box** 是必须的。如果遇到 "tagging" 或 "packaging" 相关的错误，请检查 MP4Box 是否正确安装并配置了环境变量.
- **ALAC/Atmos** 下载需要配合解密 Wrapper 使用.
- **歌词** 和 **MV** 下载需要有效的 `media-user-token` (且账号需有订阅).

## 📜 免责声明

本工具仅供学习和教育目的使用。请支持艺术家，在官方平台流媒体播放或购买音乐。
