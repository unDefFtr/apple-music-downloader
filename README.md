# Apple Music Downloader

A powerful command-line tool to download Apple Music songs, albums, playlists, stations, and music videos in **ALAC** (Lossless / Hi-Res), **Dolby Atmos**, and **AAC** formats.

## ✨ Features

- **High Quality**: Support for ALAC (Lossless / Hi-Res), Dolby Atmos (E-AC-3), and AAC (256kbps).
- **Media Support**: Download Songs, Albums, Playlists, Stations, Artists, and Music Videos (up to 4K).
- **Interactive Search**: Search for albums, songs, or artists directly from the CLI.
- **Metadata**: Automatically tags files with metadata, including lyrics (synced/unsynced/translation) and cover art.
- **Extras**: Support for animated artwork (requires ffmpeg) and format conversion.

## 🛠️ Prerequisites

Before using this tool, ensure you have the following installed:

1.  **Go**: To run the downloader.
2.  **[MP4Box (GPAC)](https://gpac.io/downloads/gpac-nightly-builds/)**: **Required** for packaging and tagging files. Ensure it's in your system PATH.
3.  **[mp4decrypt (Bento4)](https://www.bento4.com/downloads/)**: **Optional** (Required ONLY for downloading Music Videos).
4.  **[ffmpeg](https://ffmpeg.org/download.html)**: **Optional** (Required for format conversion and animated artwork).
5.  **Decryption Wrapper**: A running instance of the decryption wrapper (e.g., [wrapper](https://github.com/WorldObservationLog/wrapper)) is required for ALAC and Dolby Atmos downloads.

## 🚀 Installation & Setup

1.  **Clone the repository**:
    ```bash
    git clone https://github.com/unDefFtr/apple-music-downloader.git
    cd apple-music-downloader
    ```

2.  **Configuration**:
    Copy the example config and edit it:
    ```bash
    cp config.example.yaml config.yaml
    ```
    *   **media-user-token**: Required for downloading AAC-LC, MVs, and Lyrics. See "How to get media-user-token" below.
    *   **decrypt-m3u8-port**: Set to the address of your decryption wrapper (default is usually correct if running locally).

## 📖 Usage

### Basic Usage

Run the downloader with a URL:

```bash
go run main.go [URL]
```

### Search Mode (Interactive)

Search for media interactively:

```bash
go run main.go --search [album|song|artist] "your search query"
```
*Example*: `go run main.go --search artist "Taylor Swift"`

### Command Line Flags

| Flag | Description |
| :--- | :--- |
| `--search [type]` | Search mode (`album`, `song`, `artist`). |
| `--song` | Download a single song from an album URL. |
| `--select` | Interactively select songs to download from an album/playlist. |
| `--all-album` | Download all albums when an artist URL is provided. |
| `--atmos` | Download in **Dolby Atmos** format. |
| `--aac` | Download in **AAC** format. |
| `--debug` | Enable debug mode (shows quality info). |
| `--thread [num]` | Set number of download threads (default: 1). |

### Examples

**Download an Album:**
```bash
go run main.go https://music.apple.com/us/album/1989-taylors-version/1708308989
```

**Download a Single Song:**
```bash
go run main.go --song https://music.apple.com/us/album/cruel-summer/1468027721?i=1468027728
```

**Select Specific Tracks:**
```bash
go run main.go --select https://music.apple.com/us/album/midnights/1649438810
```

**Download Dolby Atmos:**
```bash
go run main.go --atmos https://music.apple.com/us/album/1989-taylors-version/1708308989
```

**Download Music Video:**
```bash
go run main.go https://music.apple.com/us/music-video/anti-hero/1649439395
```

## 🐳 Docker Usage

1.  **Build the image**:
    ```bash
    docker build -t apple-music-dl .
    ```

2.  **Run**:
    Ensure your config is set up and the wrapper is accessible.
    ```bash
    docker run --network host -v $(pwd)/downloads:/downloads apple-music-dl [URL/Flags]
    ```

## 🔑 How to get `media-user-token`

1.  Open [Apple Music](https://music.apple.com) in your browser and log in.
2.  Open Developer Tools (F12) -> Application (or Storage) -> Cookies.
3.  Look for `https://music.apple.com` and find the cookie named `media-user-token`.
4.  Copy the value and paste it into your `config.yaml`.

## 📝 Notes

- **MP4Box** is critical. If you see errors about "tagging" or "packaging", check your MP4Box installation.
- **ALAC/Atmos** requires the decryption wrapper to be running.
- **Lyrics** and **MVs** require a valid `media-user-token` with an active subscription.

## 📜 Disclaimer

This tool is for educational purposes only. Please support the artists by streaming or purchasing their music on official platforms.
