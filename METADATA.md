# Apple Music API Integration Guide

This document details the reverse-engineered Apple Music API endpoints used for metadata retrieval. It provides the specific HTTP requests required to interact with the API, including necessary headers and parameters.

> **Note**: Replace `[YOUR_BEARER_TOKEN]` and `[YOUR_MEDIA_USER_TOKEN]` with actual valid tokens. The `storefront` (e.g., `us`, `cn`, `jp`) in the URL path should be adjusted based on the target region.

## Authentication

### 1. Bearer Token (Public)
The API requires a JWT Bearer token, which is short-lived and typically extracted from the Apple Music web player source code.

*   **Method**: Web Scraping
*   **Logic**:
    1.  Request `https://music.apple.com`.
    2.  Locate the main JavaScript bundle URL (pattern: `/assets/index~....js`).
    3.  Fetch the JS file.
    4.  Extract the token string (starts with `eyJh...`).
*   **Usage**: Header `Authorization: Bearer [TOKEN]`

### 2. Media User Token (Private)
Required for accessing full lyrics and personalized radio stations. This corresponds to a logged-in user session.

*   **Source**: Extracted from browser cookies (`media-user-token`) after logging into Apple Music.
*   **Usage**:
    *   **Lyrics**: Passed as a Cookie.
    *   **Stations**: Passed as a Request Header.

---

## API Endpoints

### 1. Get Album Details
Retrieves metadata for an album, including its tracklist, artists, and record label info.

*   **URL**: `https://amp-api.music.apple.com/v1/catalog/{storefront}/albums/{id}`
*   **Method**: `GET`

**Query Parameters:**
*   `omit[resource]`: `autos` (Reduces payload size)
*   `include`: `tracks,artists,record-labels`
*   `include[songs]`: `artists` (Include artist info for each track)
*   `extend`: `editorialVideo,extendedAssetUrls` (Required for high-res assets)
*   `l`: Language code (e.g., `en-US`)

**Curl Example:**
```bash
curl "https://amp-api.music.apple.com/v1/catalog/us/albums/1553258783?omit%5Bresource%5D=autos&include=tracks,artists,record-labels&include%5Bsongs%5D=artists&extend=editorialVideo,extendedAssetUrls&l=en-US" \
  -H "Authorization: Bearer [YOUR_BEARER_TOKEN]" \
  -H "Origin: https://music.apple.com" \
  -H "User-Agent: Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36"
```

### 2. Get Song Details
Retrieves metadata for a single song. The `extendedAssetUrls` parameter is critical for obtaining `enhancedHls` and other media URLs.

*   **URL**: `https://amp-api.music.apple.com/v1/catalog/{storefront}/songs/{id}`
*   **Method**: `GET`

**Query Parameters:**
*   `include`: `albums,artists`
*   `extend`: `extendedAssetUrls`
*   `l`: `en-US`

**Curl Example:**
```bash
curl "https://amp-api.music.apple.com/v1/catalog/us/songs/1553258787?include=albums,artists&extend=extendedAssetUrls&l=en-US" \
  -H "Authorization: Bearer [YOUR_BEARER_TOKEN]" \
  -H "Origin: https://music.apple.com" \
  -H "User-Agent: Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36"
```

### 3. Get Lyrics
Fetches time-synced lyrics in TTML format. Requires a valid user session.

*   **URL**: `https://amp-api.music.apple.com/v1/catalog/{storefront}/songs/{songId}/{type}`
    *   `{type}` can be `lyrics` or `syllable-lyrics`.
*   **Method**: `GET`

**Query Parameters:**
*   `extend`: `ttmlLocalizations`
*   `l`: `en-US`

**Headers:**
*   `Cookie`: `media-user-token=[YOUR_MEDIA_USER_TOKEN]`
*   `Referer`: `https://music.apple.com/` (Strictly required)

**Curl Example:**
```bash
curl "https://amp-api.music.apple.com/v1/catalog/us/songs/1553258787/lyrics?extend=ttmlLocalizations&l=en-US" \
  -H "Authorization: Bearer [YOUR_BEARER_TOKEN]" \
  -H "Origin: https://music.apple.com" \
  -H "Referer: https://music.apple.com/" \
  -H "Cookie: media-user-token=[YOUR_MEDIA_USER_TOKEN]" \
  -H "User-Agent: Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36"
```

### 4. Get Playlist
Retrieves playlist metadata and its tracks.

*   **URL**: `https://amp-api.music.apple.com/v1/catalog/{storefront}/playlists/{id}`
*   **Method**: `GET`

**Query Parameters:**
*   `omit[resource]`: `autos`
*   `include`: `tracks,artists,record-labels`
*   `include[songs]`: `artists`
*   `extend`: `editorialVideo,extendedAssetUrls`
*   `l`: `en-US`

**Curl Example:**
```bash
curl "https://amp-api.music.apple.com/v1/catalog/us/playlists/pl.ba2404fbc4464b8ba2d60399189cf24e?omit%5Bresource%5D=autos&include=tracks,artists,record-labels&include%5Bsongs%5D=artists&extend=editorialVideo,extendedAssetUrls&l=en-US" \
  -H "Authorization: Bearer [YOUR_BEARER_TOKEN]" \
  -H "Origin: https://music.apple.com" \
  -H "User-Agent: Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36"
```

### 5. Search
Searches for content in the Apple Music catalog.

*   **URL**: `https://amp-api.music.apple.com/v1/catalog/{storefront}/search`
*   **Method**: `GET`

**Query Parameters:**
*   `term`: URL-encoded search string
*   `types`: Comma-separated list (e.g., `songs,albums,artists`)
*   `limit`: Integer (e.g., `10`)
*   `offset`: Integer for pagination
*   `l`: `en-US`

**Curl Example:**
```bash
curl "https://amp-api.music.apple.com/v1/catalog/us/search?term=linkin+park&types=songs,albums&limit=5&l=en-US" \
  -H "Authorization: Bearer [YOUR_BEARER_TOKEN]" \
  -H "Origin: https://music.apple.com" \
  -H "User-Agent: Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36"
```

### 6. Station Next Tracks
Generates the next batch of tracks for a radio station.

*   **URL**: `https://amp-api.music.apple.com/v1/me/stations/next-tracks/{id}`
*   **Method**: `POST`

**Query Parameters:**
*   `limit`: `10`
*   `include[songs]`: `artists,albums`
*   `extend`: `editorialVideo,extendedAssetUrls`

**Headers:**
*   `Media-User-Token`: `[YOUR_MEDIA_USER_TOKEN]`

**Curl Example:**
```bash
curl -X POST "https://amp-api.music.apple.com/v1/me/stations/next-tracks/ra.985484166?limit=10&include%5Bsongs%5D=artists,albums&extend=editorialVideo,extendedAssetUrls" \
  -H "Authorization: Bearer [YOUR_BEARER_TOKEN]" \
  -H "Media-User-Token: [YOUR_MEDIA_USER_TOKEN]" \
  -H "Origin: https://music.apple.com" \
  -H "User-Agent: Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36"
```
