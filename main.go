package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"main/utils/ampapi"
	"main/utils/logger"
	"main/utils/lyrics"
	"main/utils/runv2"
	"main/utils/runv3"
	"main/utils/structs"
	"main/utils/task"
	"main/utils/tui"

	"github.com/AlecAivazis/survey/v2"
	"github.com/fatih/color"
	"github.com/grafov/m3u8"
	"github.com/olekukonko/tablewriter"
	"github.com/spf13/pflag"
	"github.com/zhaarey/go-mp4tag"
	"gopkg.in/yaml.v2"
)

var (
	forbiddenNames = regexp.MustCompile(`[/\\<>:"|?*]`)
	dl_atmos       bool
	dl_aac         bool
	dl_select      bool
	dl_song        bool
	artist_select  bool
	debug_mode     bool
	alac_max       *int
	atmos_max      *int
	mv_max         *int
	mv_audio_type  *string
	aac_type       *string
	Config         structs.ConfigSet
	counter        structs.Counter
	okDict         = make(map[string][]int)
	mu             sync.Mutex
	thread_num     *int
	taskTotal      int
	decryptSem     chan struct{} // Semaphore for decryption
	coverFile      bool
	coverName      string
	coverDisabled  bool
)

func LimitString(s string) string {
	if len([]rune(s)) > Config.Naming.LimitMax {
		return string([]rune(s)[:Config.Naming.LimitMax])
	}
	return s
}

func isInArray(arr []int, target int) bool {
	for _, num := range arr {
		if num == target {
			return true
		}
	}
	return false
}

func fileExists(path string) (bool, error) {
	f, err := os.Stat(path)
	if err == nil {
		return !f.IsDir(), nil
	} else if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func checkUrl(url string) (string, string) {
	pat := regexp.MustCompile(`^(?:https:\/\/(?:beta\.music|music|classical\.music)\.apple\.com\/(\w{2})(?:\/album|\/album\/.+))\/(?:id)?(\d[^\D]+)(?:$|\?)`)
	matches := pat.FindAllStringSubmatch(url, -1)

	if matches == nil {
		return "", ""
	} else {
		return matches[0][1], matches[0][2]
	}
}
func checkUrlMv(url string) (string, string) {
	pat := regexp.MustCompile(`^(?:https:\/\/(?:beta\.music|music)\.apple\.com\/(\w{2})(?:\/music-video|\/music-video\/.+))\/(?:id)?(\d[^\D]+)(?:$|\?)`)
	matches := pat.FindAllStringSubmatch(url, -1)

	if matches == nil {
		return "", ""
	} else {
		return matches[0][1], matches[0][2]
	}
}
func checkUrlSong(url string) (string, string) {
	pat := regexp.MustCompile(`^(?:https:\/\/(?:beta\.music|music|classical\.music)\.apple\.com\/(\w{2})(?:\/song|\/song\/.+))\/(?:id)?(\d[^\D]+)(?:$|\?)`)
	matches := pat.FindAllStringSubmatch(url, -1)

	if matches == nil {
		return "", ""
	} else {
		return matches[0][1], matches[0][2]
	}
}
func checkUrlPlaylist(url string) (string, string) {
	pat := regexp.MustCompile(`^(?:https:\/\/(?:beta\.music|music|classical\.music)\.apple\.com\/(\w{2})(?:\/playlist|\/playlist\/.+))\/(?:id)?(pl\.[\w-]+)(?:$|\?)`)
	matches := pat.FindAllStringSubmatch(url, -1)

	if matches == nil {
		return "", ""
	} else {
		return matches[0][1], matches[0][2]
	}
}

func checkUrlStation(url string) (string, string) {
	pat := regexp.MustCompile(`^(?:https:\/\/(?:beta\.music|music)\.apple\.com\/(\w{2})(?:\/station|\/station\/.+))\/(?:id)?(ra\.[\w-]+)(?:$|\?)`)
	matches := pat.FindAllStringSubmatch(url, -1)

	if matches == nil {
		return "", ""
	} else {
		return matches[0][1], matches[0][2]
	}
}

func checkUrlArtist(url string) (string, string) {
	pat := regexp.MustCompile(`^(?:https:\/\/(?:beta\.music|music|classical\.music)\.apple\.com\/(\w{2})(?:\/artist|\/artist\/.+))\/(?:id)?(\d[^\D]+)(?:$|\?)`)
	matches := pat.FindAllStringSubmatch(url, -1)

	if matches == nil {
		return "", ""
	} else {
		return matches[0][1], matches[0][2]
	}
}
func getUrlSong(songUrl string, token string) (string, error) {
	storefront, songId := checkUrlSong(songUrl)
	manifest, err := ampapi.GetSongResp(storefront, songId, Config.Auth.Language, token)
	if err != nil {
		logger.Errorf("Failed to get manifest: %v", err)
		counter.NotSong++
		return "", err
	}
	albumId := manifest.Data[0].Relationships.Albums.Data[0].ID
	songAlbumUrl := fmt.Sprintf("https://music.apple.com/%s/album/1/%s?i=%s", storefront, albumId, songId)
	return songAlbumUrl, nil
}
func getUrlArtistName(artistUrl string, token string) (string, string, error) {
	storefront, artistId := checkUrlArtist(artistUrl)
	req, err := http.NewRequest("GET", fmt.Sprintf("https://amp-api.music.apple.com/v1/catalog/%s/artists/%s", storefront, artistId), nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")
	req.Header.Set("Origin", "https://music.apple.com")
	query := url.Values{}
	query.Set("l", Config.Auth.Language)
	req.URL.RawQuery = query.Encode()
	do, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer do.Body.Close()
	if do.StatusCode != http.StatusOK {
		return "", "", errors.New(do.Status)
	}
	obj := new(structs.AutoGeneratedArtist)
	err = json.NewDecoder(do.Body).Decode(&obj)
	if err != nil {
		return "", "", err
	}
	return obj.Data[0].Attributes.Name, obj.Data[0].ID, nil
}

func checkArtist(artistUrl string, token string, relationship string) ([]string, error) {
	storefront, artistId := checkUrlArtist(artistUrl)
	Num := 0
	//id := 1
	var args []string
	var urls []string
	var options [][]string
	for {
		req, err := http.NewRequest("GET", fmt.Sprintf("https://amp-api.music.apple.com/v1/catalog/%s/artists/%s/%s?limit=100&offset=%d&l=%s", storefront, artistId, relationship, Num, Config.Auth.Language), nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")
		req.Header.Set("Origin", "https://music.apple.com")
		do, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer do.Body.Close()
		if do.StatusCode != http.StatusOK {
			return nil, errors.New(do.Status)
		}
		obj := new(structs.AutoGeneratedArtist)
		err = json.NewDecoder(do.Body).Decode(&obj)
		if err != nil {
			return nil, err
		}
		for _, album := range obj.Data {
			options = append(options, []string{album.Attributes.Name, album.Attributes.ReleaseDate, album.ID, album.Attributes.URL})
		}
		Num = Num + 100
		if len(obj.Next) == 0 {
			break
		}
	}
	sort.Slice(options, func(i, j int) bool {
		// 将日期字符串解析为 time.Time 类型进行比较
		dateI, _ := time.Parse("2006-01-02", options[i][1])
		dateJ, _ := time.Parse("2006-01-02", options[j][1])
		return dateI.Before(dateJ) // 返回 true 表示 i 在 j 前面
	})

	table := tablewriter.NewWriter(os.Stdout)
	if relationship == "albums" {
		table.SetHeader([]string{"", "Album Name", "Date", "Album ID"})
	} else if relationship == "music-videos" {
		table.SetHeader([]string{"", "MV Name", "Date", "MV ID"})
	}
	table.SetRowLine(false)
	table.SetHeaderColor(tablewriter.Colors{},
		tablewriter.Colors{tablewriter.FgRedColor, tablewriter.Bold},
		tablewriter.Colors{tablewriter.Bold, tablewriter.FgBlackColor},
		tablewriter.Colors{tablewriter.Bold, tablewriter.FgBlackColor})

	table.SetColumnColor(tablewriter.Colors{tablewriter.FgCyanColor},
		tablewriter.Colors{tablewriter.Bold, tablewriter.FgRedColor},
		tablewriter.Colors{tablewriter.Bold, tablewriter.FgBlackColor},
		tablewriter.Colors{tablewriter.Bold, tablewriter.FgBlackColor})
	for i, v := range options {
		urls = append(urls, v[3])
		options[i] = append([]string{fmt.Sprint(i + 1)}, v[:3]...)
		table.Append(options[i])
	}
	table.Render()
	if artist_select {
		fmt.Println("You have selected all options:")
		return urls, nil
	}
	reader := bufio.NewReader(os.Stdin)
	fmt.Println("Please select from the " + relationship + " options above (multiple options separated by commas, ranges supported, or type 'all' to select all)")
	cyanColor := color.New(color.FgCyan)
	cyanColor.Print("Enter your choice: ")
	input, _ := reader.ReadString('\n')

	input = strings.TrimSpace(input)
	if input == "all" {
		fmt.Println("You have selected all options:")
		return urls, nil
	}

	selectedOptions := [][]string{}
	parts := strings.Split(input, ",")
	for _, part := range parts {
		if strings.Contains(part, "-") {
			rangeParts := strings.Split(part, "-")
			selectedOptions = append(selectedOptions, rangeParts)
		} else {
			selectedOptions = append(selectedOptions, []string{part})
		}
	}

	fmt.Println("You have selected the following options:")
	for _, opt := range selectedOptions {
		if len(opt) == 1 {
			num, err := strconv.Atoi(opt[0])
			if err != nil {
				fmt.Println("Invalid option:", opt[0])
				continue
			}
			if num > 0 && num <= len(options) {
				fmt.Println(options[num-1])
				args = append(args, urls[num-1])
			} else {
				fmt.Println("Option out of range:", opt[0])
			}
		} else if len(opt) == 2 {
			start, err1 := strconv.Atoi(opt[0])
			end, err2 := strconv.Atoi(opt[1])
			if err1 != nil || err2 != nil {
				fmt.Println("Invalid range:", opt)
				continue
			}
			if start < 1 || end > len(options) || start > end {
				fmt.Println("Range out of range:", opt)
				continue
			}
			for i := start; i <= end; i++ {
				fmt.Println(options[i-1])
				args = append(args, urls[i-1])
			}
		} else {
			fmt.Println("Invalid option:", opt)
		}
	}
	return args, nil
}

func writeCover(sanAlbumFolder, name string, url string) (string, error) {
	originalUrl := url
	var ext string
	var covPath string
	if Config.Artwork.Format == "original" {
		ext = strings.Split(url, "/")[len(strings.Split(url, "/"))-2]
		ext = ext[strings.LastIndex(ext, ".")+1:]
		covPath = filepath.Join(sanAlbumFolder, name+"."+ext)
	} else {
		covPath = filepath.Join(sanAlbumFolder, name+"."+Config.Artwork.Format)
	}
	exists, err := fileExists(covPath)
	if err != nil {
		logger.Error("Failed to check if cover exists.")
		return "", err
	}
	if exists {
		_ = os.Remove(covPath)
	}
	if Config.Artwork.Format == "png" {
		re := regexp.MustCompile(`\{w\}x\{h\}`)
		parts := re.Split(url, 2)
		url = parts[0] + "{w}x{h}" + strings.Replace(parts[1], ".jpg", ".png", 1)
	}
	url = strings.Replace(url, "{w}x{h}", Config.Artwork.Size, 1)
	if Config.Artwork.Format == "original" {
		url = strings.Replace(url, "is1-ssl.mzstatic.com/image/thumb", "a5.mzstatic.com/us/r1000/0", 1)
		url = url[:strings.LastIndex(url, "/")]
	}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")
	do, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer do.Body.Close()
	if do.StatusCode != http.StatusOK {
		if Config.Artwork.Format == "original" {
			logger.Warn("Failed to get cover, falling back to " + ext + " url.")
			splitByDot := strings.Split(originalUrl, ".")
			last := splitByDot[len(splitByDot)-1]
			fallback := originalUrl[:len(originalUrl)-len(last)] + ext
			fallback = strings.Replace(fallback, "{w}x{h}", Config.Artwork.Size, 1)
			logger.Infof("Fallback URL: %s", fallback)
			req, err = http.NewRequest("GET", fallback, nil)
			if err != nil {
				logger.Error("Failed to create request for fallback url.")
				return "", err
			}
			req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")
			do, err = http.DefaultClient.Do(req)
			if err != nil {
				logger.Error("Failed to get cover from fallback url.")
				return "", err
			}
			defer do.Body.Close()
			if do.StatusCode != http.StatusOK {
				logger.Info(fallback)
				return "", errors.New(do.Status)
			}
		} else {
			return "", errors.New(do.Status)
		}
	}
	f, err := os.Create(covPath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	_, err = io.Copy(f, do.Body)
	if err != nil {
		return "", err
	}
	return covPath, nil
}

func writeLyrics(sanAlbumFolder, filename string, lrc string) error {
	lyricspath := filepath.Join(sanAlbumFolder, filename)
	f, err := os.Create(lyricspath)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(lrc)
	if err != nil {
		return err
	}
	return nil
}

func contains(slice []string, item string) bool {
	for _, v := range slice {
		if v == item {
			return true
		}
	}
	return false
}

// START: New functions for search functionality

// SearchResultItem is a unified struct to hold search results for display.
type SearchResultItem struct {
	Type   string
	Name   string
	Detail string
	URL    string
	ID     string
}

// QualityOption holds information about a downloadable quality.
type QualityOption struct {
	ID          string
	Description string
}

// setDlFlags configures the global download flags based on the user's quality selection.
func setDlFlags(quality string) {
	dl_atmos = false
	dl_aac = false

	switch quality {
	case "atmos":
		dl_atmos = true
		logger.Info("Quality set to: Dolby Atmos")
	case "aac":
		dl_aac = true
		*aac_type = "aac"
		logger.Info("Quality set to: High-Quality (AAC)")
	case "alac":
		logger.Info("Quality set to: Lossless (ALAC)")
	}
}

// promptForQuality asks the user to select a download quality for the chosen media.
func promptForQuality(item SearchResultItem, token string) (string, error) {
	if item.Type == "Artist" {
		logger.Info("Artist selected. Proceeding to list all albums/videos.")
		return "default", nil
	}

	logger.Infof("Fetching available qualities for: %s", item.Name)

	qualities := []QualityOption{
		{ID: "alac", Description: "Lossless (ALAC)"},
		{ID: "aac", Description: "High-Quality (AAC)"},
		{ID: "atmos", Description: "Dolby Atmos"},
	}
	qualityOptions := []string{}
	for _, q := range qualities {
		qualityOptions = append(qualityOptions, q.Description)
	}

	prompt := &survey.Select{
		Message:  "Select a quality to download:",
		Options:  qualityOptions,
		PageSize: 5,
	}

	selectedIndex := 0
	err := survey.AskOne(prompt, &selectedIndex)
	if err != nil {
		// This can happen if the user presses Ctrl+C
		return "", nil
	}

	return qualities[selectedIndex].ID, nil
}

// handleSearch manages the entire interactive search process.
func handleSearch(searchType string, queryParts []string, token string) (string, error) {
	query := strings.Join(queryParts, " ")
	validTypes := map[string]bool{"album": true, "song": true, "artist": true}
	if !validTypes[searchType] {
		return "", fmt.Errorf("invalid search type: %s. Use 'album', 'song', or 'artist'", searchType)
	}

	logger.Infof("Searching for %ss: \"%s\" in storefront \"%s\"", searchType, query, Config.Auth.Storefront)

	offset := 0
	limit := 15 // Increased limit for better navigation

	apiSearchType := searchType + "s"

	for {
		searchResp, err := ampapi.Search(Config.Auth.Storefront, query, apiSearchType, Config.Auth.Language, token, limit, offset)
		if err != nil {
			return "", fmt.Errorf("error fetching search results: %w", err)
		}

		var items []SearchResultItem
		var displayOptions []string
		hasNext := false

		// Special options for navigation
		const prevPageOpt = "⬅️  Previous Page"
		const nextPageOpt = "➡️  Next Page"

		// Add previous page option if applicable
		if offset > 0 {
			displayOptions = append(displayOptions, prevPageOpt)
		}

		switch searchType {
		case "album":
			if searchResp.Results.Albums != nil {
				for _, item := range searchResp.Results.Albums.Data {
					year := ""
					if len(item.Attributes.ReleaseDate) >= 4 {
						year = item.Attributes.ReleaseDate[:4]
					}
					trackInfo := fmt.Sprintf("%d tracks", item.Attributes.TrackCount)
					detail := fmt.Sprintf("%s (%s, %s)", item.Attributes.ArtistName, year, trackInfo)
					displayOptions = append(displayOptions, fmt.Sprintf("%s - %s", item.Attributes.Name, detail))
					items = append(items, SearchResultItem{Type: "Album", URL: item.Attributes.URL, ID: item.ID})
				}
				hasNext = searchResp.Results.Albums.Next != ""
			}
		case "song":
			if searchResp.Results.Songs != nil {
				for _, item := range searchResp.Results.Songs.Data {
					detail := fmt.Sprintf("%s (%s)", item.Attributes.ArtistName, item.Attributes.AlbumName)
					displayOptions = append(displayOptions, fmt.Sprintf("%s - %s", item.Attributes.Name, detail))
					items = append(items, SearchResultItem{Type: "Song", URL: item.Attributes.URL, ID: item.ID})
				}
				hasNext = searchResp.Results.Songs.Next != ""
			}
		case "artist":
			if searchResp.Results.Artists != nil {
				for _, item := range searchResp.Results.Artists.Data {
					detail := ""
					if len(item.Attributes.GenreNames) > 0 {
						detail = strings.Join(item.Attributes.GenreNames, ", ")
					}
					displayOptions = append(displayOptions, fmt.Sprintf("%s (%s)", item.Attributes.Name, detail))
					items = append(items, SearchResultItem{Type: "Artist", URL: item.Attributes.URL, ID: item.ID})
				}
				hasNext = searchResp.Results.Artists.Next != ""
			}
		}

		if len(items) == 0 && offset == 0 {
			logger.Info("No results found.")
			return "", nil
		}

		// Add next page option if applicable
		if hasNext {
			displayOptions = append(displayOptions, nextPageOpt)
		}

		prompt := &survey.Select{
			Message:  "Use arrow keys to navigate, Enter to select:",
			Options:  displayOptions,
			PageSize: limit, // Show a full page of results
		}

		selectedIndex := 0
		err = survey.AskOne(prompt, &selectedIndex)
		if err != nil {
			// User pressed Ctrl+C
			return "", nil
		}

		selectedOption := displayOptions[selectedIndex]

		// Handle pagination
		if selectedOption == nextPageOpt {
			offset += limit
			continue
		}
		if selectedOption == prevPageOpt {
			offset -= limit
			continue
		}

		// Adjust index to match the `items` slice if "Previous Page" was an option
		itemIndex := selectedIndex
		if offset > 0 {
			itemIndex--
		}

		selectedItem := items[itemIndex]

		// Automatically set single song download flag
		if selectedItem.Type == "Song" {
			dl_song = true
		}

		quality, err := promptForQuality(selectedItem, token)
		if err != nil {
			return "", fmt.Errorf("could not process quality selection: %w", err)
		}
		if quality == "" { // User cancelled quality selection
			logger.Info("Selection cancelled.")
			return "", nil
		}

		if quality != "default" {
			setDlFlags(quality)
		}

		return selectedItem.URL, nil
	}
}

// END: New functions for search functionality

// CONVERSION FEATURE: Determine if source codec is lossy (rough heuristic by extension/codec name).
func isLossySource(ext string, codec string) bool {
	ext = strings.ToLower(ext)
	if ext == ".m4a" && (codec == "AAC" || strings.Contains(codec, "AAC") || strings.Contains(codec, "ATMOS")) {
		return true
	}
	if ext == ".mp3" || ext == ".opus" || ext == ".ogg" {
		return true
	}
	return false
}

// CONVERSION FEATURE: Build ffmpeg arguments for desired target.
func buildFFmpegArgs(ffmpegPath, inPath, outPath, targetFmt, extraArgs string) ([]string, error) {
	args := []string{"-y", "-i", inPath, "-vn"}
	switch targetFmt {
	case "flac":
		args = append(args, "-c:a", "flac")
	case "mp3":
		// VBR quality 2 ~ high quality
		args = append(args, "-c:a", "libmp3lame", "-qscale:a", "2")
	case "opus":
		// Medium/high quality
		args = append(args, "-c:a", "libopus", "-b:a", "192k", "-vbr", "on")
	case "wav":
		args = append(args, "-c:a", "pcm_s16le")
	case "copy":
		// Just container copy (probably pointless for same container)
		args = append(args, "-c", "copy")
	default:
		return nil, fmt.Errorf("unsupported convert-format: %s", targetFmt)
	}
	if extraArgs != "" {
		// naive split; for complex quoting you could enhance
		args = append(args, strings.Fields(extraArgs)...)
	}
	args = append(args, outPath)
	return args, nil
}

// CONVERSION FEATURE: Perform conversion if enabled.
func convertIfNeeded(track *task.Track) {
	if !Config.Convert.Enable {
		return
	}
	if Config.Convert.Format == "" {
		return
	}
	srcPath := track.SavePath
	if srcPath == "" {
		return
	}
	ext := strings.ToLower(filepath.Ext(srcPath))
	targetFmt := strings.ToLower(Config.Convert.Format)

	// Map extension for output
	if targetFmt == "copy" {
		logger.Info("Convert (copy) requested; skipping because it produces no new format.")
		return
	}

	if Config.Convert.SkipIfSourceMatches {
		if ext == "."+targetFmt {
			logger.Infof("Conversion skipped (already %s)", targetFmt)
			return
		}
	}

	outBase := strings.TrimSuffix(srcPath, ext)
	outPath := outBase + "." + targetFmt

	// Handle lossy -> lossless cases: optionally skip or warn
	if (targetFmt == "flac" || targetFmt == "wav") && isLossySource(ext, track.Codec) {
		if Config.Convert.SkipLossyToLossless {
			logger.Info("Skipping conversion: source appears lossy and target is lossless; configured to skip.")
			return
		}
		if Config.Convert.WarnLossyToLossless {
			logger.Warn("Warning: Converting lossy source to lossless container will not improve quality.")
		}
	}

	if _, err := exec.LookPath(Config.Convert.FFmpegPath); err != nil {
		logger.Warnf("ffmpeg not found at '%s'; skipping conversion.", Config.Convert.FFmpegPath)
		return
	}

	args, err := buildFFmpegArgs(Config.Convert.FFmpegPath, srcPath, outPath, targetFmt, Config.Convert.ExtraArgs)
	if err != nil {
		logger.Errorf("Conversion config error: %v", err)
		return
	}

	logger.Infof("Converting -> %s ...\n", targetFmt)
	cmd := exec.Command(Config.Convert.FFmpegPath, args...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	start := time.Now()
	if err := cmd.Run(); err != nil {
		logger.Error("Conversion failed:", err)
		// leave original
		return
	}
	logger.Infof("Conversion completed in %s: %s", time.Since(start).Truncate(time.Millisecond), filepath.Base(outPath))

	if !Config.Convert.KeepOriginal {
		if err := os.Remove(srcPath); err != nil {
			logger.Errorf("Failed to remove original after conversion: %v", err)
		} else {
			track.SavePath = outPath
			track.SaveName = filepath.Base(outPath)
			logger.Info("Original removed.")
		}
	} else {
		// Keep both but point track to new file (optional decision)
		track.SavePath = outPath
		track.SaveName = filepath.Base(outPath)
	}
}

func ripTrack(track *task.Track, token string, mediaUserToken string, downloadSem chan struct{}) {
	// Acquire download sem if not already acquired?
	// The caller passes downloadSem. We assume caller ACQUIRED it.
	// We will release it when download is done.
	defer func() {
		// Ensure we drain the semaphore if we exit early
		// But wait, if we spawn a goroutine for decrypt, we shouldn't drain it here if we passed it?
		// No, ripTrack logic:
		// 1. Download (holding downloadSem)
		// 2. Release downloadSem
		// 3. Acquire decryptSem
		// 4. Decrypt
		// 5. Release decryptSem
	}()

	tui.AddTask(track.ID, track.Name)
	incrementTotal()
	logger.Infof("Track %d of %d: %s\n", track.TaskNum, track.TaskTotal, track.Type)

	var err error
	var dCtx *runv2.DecryptionContext
	var tempFile string

	maxRetries := 3

	// Phase 1: Download
	for i := 0; i <= maxRetries; i++ {
		// Define callback for download phase
		cb := func(p float64, msg string, speed float64) {
			tui.UpdateTask(track.ID, p, msg, "downloading", speed)
		}

		dCtx, tempFile, err = doRipTrackDownload(track, token, mediaUserToken, cb)
		if err == nil {
			break
		}

		if err.Error() == "Unavailable" || err.Error() == "Invalid media-user-token" {
			break
		}

		if i == maxRetries {
			break
		}

		logger.Warnf("Task %s Download failed: %v. Retrying (%d/%d)...", track.Name, err, i+1, maxRetries)
		tui.UpdateTask(track.ID, 0, fmt.Sprintf("Download Retry (%d/%d)...", i+1, maxRetries), "downloading", 0)
		time.Sleep(2 * time.Second)
	}

	// Release Download Semaphore
	<-downloadSem

	if err != nil {
		logger.Errorf("Task %s failed: %v", track.Name, err)
		tui.UpdateTask(track.ID, 0, "Failed", "error", 0)
		if err.Error() == "Unavailable" {
			incrementUnavailable()
		} else {
			incrementError()
		}
		return
	}

	// If no decryption context, it means it was a v3 track or handled otherwise (skipped/exists)
	if dCtx == nil {
		// Check if it was actually skipped or done
		// If tempFile is empty, it might mean it exists or v3 handled it
		// We should increment success here if err was nil
		incrementSuccess()
		addToOkDict(track.PreID, track.TaskNum)
		tui.UpdateTask(track.ID, 1.0, "Done", "done", 0)
		return
	}

	// Phase 2: Decrypt
	// Acquire Decrypt Semaphore
	decryptSem <- struct{}{}
	defer func() { <-decryptSem }()

	for i := 0; i <= maxRetries; i++ {
		cb := func(p float64, msg string, speed float64) {
			tui.UpdateTask(track.ID, p, msg, "decrypting", speed)
		}

		err = doRipTrackDecrypt(track, dCtx, tempFile, cb)
		if err == nil {
			break
		}

		if i == maxRetries {
			break
		}

		logger.Warnf("Task %s Decrypt failed: %v. Retrying (%d/%d)...", track.Name, err, i+1, maxRetries)
		tui.UpdateTask(track.ID, 0, fmt.Sprintf("Decrypt Retry (%d/%d)...", i+1, maxRetries), "decrypting", 0)
		time.Sleep(2 * time.Second)
	}

	// Clean up temp file
	os.Remove(tempFile)

	if err != nil {
		logger.Errorf("Task %s Decryption failed: %v", track.Name, err)
		tui.UpdateTask(track.ID, 0, "Decrypt Failed", "error", 0)
		incrementError()
		return
	}

	incrementSuccess()
	addToOkDict(track.PreID, track.TaskNum)
	tui.UpdateTask(track.ID, 1.0, "Done", "done", 0)
}

// doRipTrackDownload performs the download and returns context for decryption.
// If it returns nil context and nil error, it means the track is done (e.g. skipped, existing, or v3).
func doRipTrackDownload(track *task.Track, token string, mediaUserToken string, cb structs.ProgressCallback) (*runv2.DecryptionContext, string, error) {
	//提前获取到的播放列表下track所在的专辑信息
	if track.PreType == "playlists" && Config.Download.Playlist.UseSongInfo {
		track.GetAlbumData(token)
	}

	//mv dl dev
	if track.Type == "music-videos" {
		if len(mediaUserToken) <= 50 {
			logger.Warn("media-user-token is not set, skipping MV dl")
			return nil, "", nil
		}
		if _, err := exec.LookPath("mp4decrypt"); err != nil {
			logger.Warn("mp4decrypt is not found, skipping MV dl")
			return nil, "", nil
		}
		err := mvDownloader(track.ID, track.SaveDir, token, track.Storefront, mediaUserToken, track, cb)
		if err != nil {
			logger.Errorf("Failed to dl MV: %v", err)
			return nil, "", err
		}
		return nil, "", nil
	}

	needDlAacLc := false
	if dl_aac && Config.Download.Codec.AacType == "aac-lc" {
		needDlAacLc = true
	}
	if track.WebM3u8 == "" && !needDlAacLc {
		if dl_atmos {
			logger.Warn("Unavailable")
			return nil, "", errors.New("Unavailable")
		}
		logger.Warn("Unavailable, trying to dl aac-lc")
		needDlAacLc = true
	}
	needCheck := false

	if Config.Download.M3U8.Mode == "all" {
		needCheck = true
	} else if Config.Download.M3U8.Mode == "hires" && contains(track.Resp.Attributes.AudioTraits, "hi-res-lossless") {
		needCheck = true
	}
	var EnhancedHls_m3u8 string
	if needCheck && !needDlAacLc {
		EnhancedHls_m3u8, _ = checkM3u8(track.ID, "song")
		if strings.HasSuffix(EnhancedHls_m3u8, ".m3u8") {
			track.DeviceM3u8 = EnhancedHls_m3u8
			track.M3u8 = EnhancedHls_m3u8
		}
	}
	var Quality string
	if strings.Contains(Config.Naming.Song, "Quality") {
		if dl_atmos {
			Quality = fmt.Sprintf("%dKbps", Config.Download.Codec.AtmosMax-2000)
		} else if needDlAacLc {
			Quality = "256Kbps"
		} else {
			var err error
			_, Quality, err = extractMedia(track.M3u8, true)
			if err != nil {
				logger.Errorf("Failed to extract quality from manifest: %v", err)
				return nil, "", err
			}
		}
	}
	track.Quality = Quality

	stringsToJoin := []string{}
	if track.Resp.Attributes.IsAppleDigitalMaster {
		if Config.Naming.AppleMasterChoice != "" {
			stringsToJoin = append(stringsToJoin, Config.Naming.AppleMasterChoice)
		}
	}
	if track.Resp.Attributes.ContentRating == "explicit" {
		if Config.Naming.ExplicitChoice != "" {
			stringsToJoin = append(stringsToJoin, Config.Naming.ExplicitChoice)
		}
	}
	if track.Resp.Attributes.ContentRating == "clean" {
		if Config.Naming.CleanChoice != "" {
			stringsToJoin = append(stringsToJoin, Config.Naming.CleanChoice)
		}
	}
	Tag_string := strings.Join(stringsToJoin, " ")

	songName := strings.NewReplacer(
		"{SongId}", track.ID,
		"{SongNumer}", fmt.Sprintf("%02d", track.TaskNum),
		"{SongName}", LimitString(track.Resp.Attributes.Name),
		"{DiscNumber}", fmt.Sprintf("%0d", track.Resp.Attributes.DiscNumber),
		"{TrackNumber}", fmt.Sprintf("%0d", track.Resp.Attributes.TrackNumber),
		"{Quality}", Quality,
		"{Tag}", Tag_string,
		"{Codec}", track.Codec,
	).Replace(Config.Naming.Song)
	logger.Info(songName)
	filename := fmt.Sprintf("%s.m4a", forbiddenNames.ReplaceAllString(songName, "_"))
	track.SaveName = filename
	trackPath := filepath.Join(track.SaveDir, track.SaveName)

	// Determine possible post-conversion target file (so we can skip re-download)
	var convertedPath string
	considerConverted := false
	if Config.Convert.Enable &&
		Config.Convert.Format != "" &&
		strings.ToLower(Config.Convert.Format) != "copy" &&
		!Config.Convert.KeepOriginal {
		convertedPath = strings.TrimSuffix(trackPath, filepath.Ext(trackPath)) + "." + strings.ToLower(Config.Convert.Format)
		considerConverted = true
	}
	//get lrc
	var lrc string = ""
	if Config.Lyrics.Enable && (Config.Lyrics.Embed || Config.Lyrics.SaveFile) {
		lrcFilename := fmt.Sprintf("%s.%s", forbiddenNames.ReplaceAllString(songName, "_"), Config.Lyrics.Format)
		lrcStr, err := lyrics.Get(track.Storefront, track.ID, Config.Lyrics.Type, Config.Auth.Language, Config.Lyrics.Format, token, mediaUserToken)
		if err != nil {
			logger.Error(err)
		} else {
			if Config.Lyrics.SaveFile {
				err := writeLyrics(track.SaveDir, lrcFilename, lrcStr)
				if err != nil {
					logger.Error("Failed to write lyrics")
				}
			}
			if Config.Lyrics.Embed {
				lrc = lrcStr
			}
		}
	}
	track.Lrc = lrc // Store lrc in track for phase 2

	// Existence check now considers converted output (if original was deleted)
	existsOriginal, err := fileExists(trackPath)
	if err != nil {
		logger.Error("Failed to check if track exists.")
	}
	if existsOriginal {
		logger.Info("Track already exists locally.")
		return nil, "", nil
	}
	if considerConverted {
		existsConverted, err2 := fileExists(convertedPath)
		if err2 == nil && existsConverted {
			logger.Info("Converted track already exists locally.")
			return nil, "", nil
		}
	}

	if needDlAacLc {
		if len(mediaUserToken) <= 50 {
			logger.Error("Invalid media-user-token")
			return nil, "", errors.New("Invalid media-user-token")
		}
		_, err := runv3.Run(track.ID, trackPath, token, mediaUserToken, false, "", cb)
		if err != nil {
			logger.Errorf("Failed to dl aac-lc: %v", err)
			return nil, "", err
		}
		// AAC-LC (runv3) is done here. No Phase 2 needed (or we can move tagging there but runv3 might do it?)
		// runv3.Run calls internal DecryptMP4 which does the job.
		// However, we still need to run tagging and conversion!
		// But runv3 logic is self-contained.
		// Actually, let's look at old doRipTrack.
		// It calls runv3.Run, then proceeds to "Tags" block.
		// So we SHOULD return success here, but NO DecryptionContext, but indicate we need post-processing?
		// My refactor assumes if dCtx is nil, we are done.
		// But runv3 needs tagging.
		// Let's perform tagging/conversion here for v3 tracks for simplicity, as they don't block the decryptSem.

		tags := []string{
			"tool=",
			"artist=AppleMusic",
		}
		sharedCover := track.CoverPath != ""
		if shouldEmbedCover() {
			if (strings.Contains(track.PreID, "pl.") || strings.Contains(track.PreID, "ra.")) &&
				Config.Download.Playlist.DownloadAlbumCover &&
				track.CoverPath == "" &&
				shouldWriteCoverFile() {
				track.CoverPath, err = writeCover(track.SaveDir, track.ID, track.Resp.Attributes.Artwork.URL)
				if err != nil {
					logger.Error("Failed to write cover.")
				}
			}
			if track.CoverPath != "" {
				tags = append(tags, fmt.Sprintf("cover=%s", track.CoverPath))
			}
		}
		tagsString := strings.Join(tags, ":")
		cmd := exec.Command("MP4Box", "-itags", tagsString, trackPath)
		if err := cmd.Run(); err != nil {
			logger.Errorf("Embed failed: %v\n", err)
			return nil, "", err
		}
		if (strings.Contains(track.PreID, "pl.") || strings.Contains(track.PreID, "ra.")) &&
			Config.Download.Playlist.DownloadAlbumCover &&
			track.CoverPath != "" &&
			!sharedCover &&
			!shouldKeepCoverFile() {
			if err := os.Remove(track.CoverPath); err != nil {
				logger.Errorf("Error deleting file: %s", track.CoverPath)
			}
		}
		track.SavePath = trackPath
		err = writeMP4Tags(track, lrc)
		if err != nil {
			logger.Errorf("Failed to write tags in media: %v", err)
			return nil, "", errors.New("Unavailable")
		}
		convertIfNeeded(track)

		return nil, "", nil
	} else {
		trackM3u8Url, _, err := extractMedia(track.M3u8, false)
		if err != nil {
			logger.Errorf("Failed to extract info from manifest: %v", err)
			return nil, "", errors.New("Unavailable")
		}

		// Use a temporary file for download
		tempFile := trackPath + ".enc"
		ctx, err := runv2.Download(track.ID, trackM3u8Url, tempFile, Config, cb)
		if err != nil {
			logger.Errorf("Failed to download v2: %v", err)
			os.Remove(tempFile)
			return nil, "", err
		}

		return ctx, tempFile, nil
	}
}

func doRipTrackDecrypt(track *task.Track, dCtx *runv2.DecryptionContext, tempFile string, cb structs.ProgressCallback) error {
	trackPath := filepath.Join(track.SaveDir, track.SaveName)

	err := runv2.Decrypt(dCtx, tempFile, trackPath, cb)
	if err != nil {
		logger.Errorf("Failed to decrypt v2: %v", err)
		return err
	}

	//这里利用MP4box将fmp4转化为mp4，并添加ilst box与cover，方便后面的mp4tag添加更多自定义标签
	tags := []string{
		"tool=",
		"artist=AppleMusic",
	}
	sharedCover := track.CoverPath != ""
	if shouldEmbedCover() {
		if (strings.Contains(track.PreID, "pl.") || strings.Contains(track.PreID, "ra.")) &&
			Config.Download.Playlist.DownloadAlbumCover &&
			track.CoverPath == "" &&
			shouldWriteCoverFile() {
			track.CoverPath, err = writeCover(track.SaveDir, track.ID, track.Resp.Attributes.Artwork.URL)
			if err != nil {
				logger.Error("Failed to write cover.")
			}
		}
		if track.CoverPath != "" {
			tags = append(tags, fmt.Sprintf("cover=%s", track.CoverPath))
		}
	}
	tagsString := strings.Join(tags, ":")
	cmd := exec.Command("MP4Box", "-itags", tagsString, trackPath)
	if err := cmd.Run(); err != nil {
		logger.Errorf("Embed failed: %v\n", err)
		return err
	}
	if (strings.Contains(track.PreID, "pl.") || strings.Contains(track.PreID, "ra.")) &&
		Config.Download.Playlist.DownloadAlbumCover &&
		track.CoverPath != "" &&
		!sharedCover &&
		!shouldKeepCoverFile() {
		if err := os.Remove(track.CoverPath); err != nil {
			logger.Errorf("Error deleting file: %s", track.CoverPath)
			return err
		}
	}
	track.SavePath = trackPath
	err = writeMP4Tags(track, track.Lrc)
	if err != nil {
		logger.Errorf("Failed to write tags in media: %v", err)
		return errors.New("Unavailable")
	}

	// CONVERSION FEATURE hook
	convertIfNeeded(track)

	return nil
}

func ripStation(albumId string, token string, storefront string, mediaUserToken string) error {
	station := task.NewStation(storefront, albumId)
	err := station.GetResp(mediaUserToken, token, Config.Auth.Language)
	if err != nil {
		return err
	}
	logger.Info(" -", station.Type)
	meta := station.Resp

	var Codec string
	if dl_atmos {
		Codec = "ATMOS"
	} else if dl_aac {
		Codec = "AAC"
	} else {
		Codec = "ALAC"
	}
	station.Codec = Codec
	var singerFoldername string
	if Config.Naming.Artist != "" {
		singerFoldername = strings.NewReplacer(
			"{ArtistName}", "Apple Music Station",
			"{ArtistId}", "",
			"{UrlArtistName}", "Apple Music Station",
		).Replace(Config.Naming.Artist)
		if strings.HasSuffix(singerFoldername, ".") {
			singerFoldername = strings.ReplaceAll(singerFoldername, ".", "")
		}
		singerFoldername = strings.TrimSpace(singerFoldername)
		logger.Info(singerFoldername)
	}
	singerFolder := filepath.Join(Config.Paths.Alac, forbiddenNames.ReplaceAllString(singerFoldername, "_"))
	if dl_atmos {
		singerFolder = filepath.Join(Config.Paths.Atmos, forbiddenNames.ReplaceAllString(singerFoldername, "_"))
	}
	if dl_aac {
		singerFolder = filepath.Join(Config.Paths.Aac, forbiddenNames.ReplaceAllString(singerFoldername, "_"))
	}
	os.MkdirAll(singerFolder, os.ModePerm)
	station.SaveDir = singerFolder

	playlistFolder := strings.NewReplacer(
		"{ArtistName}", "Apple Music Station",
		"{PlaylistName}", LimitString(station.Name),
		"{PlaylistId}", station.ID,
		"{Quality}", "",
		"{Codec}", Codec,
		"{Tag}", "",
	).Replace(Config.Naming.Playlist)
	if strings.HasSuffix(playlistFolder, ".") {
		playlistFolder = strings.ReplaceAll(playlistFolder, ".", "")
	}
	playlistFolder = strings.TrimSpace(playlistFolder)
	playlistFolderPath := filepath.Join(singerFolder, forbiddenNames.ReplaceAllString(playlistFolder, "_"))
	os.MkdirAll(playlistFolderPath, os.ModePerm)
	station.SaveName = playlistFolder
	logger.Info(playlistFolder)

	var covPath string
	if shouldWriteCoverFile() {
		covPath, err = writeCover(playlistFolderPath, coverBaseName("cover"), meta.Data[0].Attributes.Artwork.URL)
		if err != nil {
			logger.Error("Failed to write cover.")
		}
	}
	station.CoverPath = covPath

	if Config.Artwork.SaveAnimated && meta.Data[0].Attributes.EditorialVideo.MotionSquare.Video != "" {
		logger.Info("Found Animation Artwork.")

		motionvideoUrlSquare, err := extractVideo(meta.Data[0].Attributes.EditorialVideo.MotionSquare.Video)
		if err != nil {
			logger.Errorf("no motion video square.\n%v", err)
		} else {
			exists, err := fileExists(filepath.Join(playlistFolderPath, "square_animated_artwork.mp4"))
			if err != nil {
				logger.Error("Failed to check if animated artwork square exists.")
			}
			if exists {
				logger.Info("Animated artwork square already exists locally.")
			} else {
				logger.Info("Animation Artwork Square Downloading...")
				cmd := exec.Command("ffmpeg", "-loglevel", "quiet", "-y", "-i", motionvideoUrlSquare, "-c", "copy", filepath.Join(playlistFolderPath, "square_animated_artwork.mp4"))
				if err := cmd.Run(); err != nil {
					logger.Errorf("animated artwork square dl err: %v\n", err)
				} else {
					logger.Info("Animation Artwork Square Downloaded")
				}
			}
		}

		if Config.Artwork.EmbyAnimated {
			cmd3 := exec.Command("ffmpeg", "-i", filepath.Join(playlistFolderPath, "square_animated_artwork.mp4"), "-vf", "scale=440:-1", "-r", "24", "-f", "gif", filepath.Join(playlistFolderPath, "folder.jpg"))
			if err := cmd3.Run(); err != nil {
				logger.Errorf("animated artwork square to gif err: %v\n", err)
			}
		}
	}
	if station.Type == "stream" {
		mu.Lock()
		taskTotal++
		mu.Unlock()
		updateTotalProgress()

		counter.Total++
		if isInArray(okDict[station.ID], 1) {
			counter.Success++
			updateTotalProgress()
			return nil
		}
		songName := strings.NewReplacer(
			"{SongId}", station.ID,
			"{SongNumer}", "01",
			"{SongName}", LimitString(station.Name),
			"{DiscNumber}", "1",
			"{TrackNumber}", "1",
			"{Quality}", "256Kbps",
			"{Tag}", "",
			"{Codec}", "AAC",
		).Replace(Config.Naming.Song)
		logger.Info(songName)
		trackPath := filepath.Join(playlistFolderPath, fmt.Sprintf("%s.m4a", forbiddenNames.ReplaceAllString(songName, "_")))
		exists, _ := fileExists(trackPath)
		if exists {
			counter.Success++
			okDict[station.ID] = append(okDict[station.ID], 1)

			logger.Info("Radio already exists locally.")
			return nil
		}
		assetsUrl, serverUrl, err := ampapi.GetStationAssetsUrlAndServerUrl(station.ID, mediaUserToken, token)
		if err != nil {
			logger.Error("Failed to get station assets url.", err)
			counter.Error++
			return err
		}
		trackM3U8 := strings.ReplaceAll(assetsUrl, "index.m3u8", "256/prog_index.m3u8")
		keyAndUrls, _ := runv3.Run(station.ID, trackM3U8, token, mediaUserToken, true, serverUrl, nil)
		err = runv3.ExtMvData(keyAndUrls, trackPath)
		if err != nil {
			logger.Error("Failed to download station stream.", err)
			counter.Error++
			return err
		}
		tags := []string{
			"tool=",
			"disk=1/1",
			"track=1",
			"tracknum=1/1",
			fmt.Sprintf("artist=%s", "Apple Music Station"),
			fmt.Sprintf("performer=%s", "Apple Music Station"),
			fmt.Sprintf("album_artist=%s", "Apple Music Station"),
			fmt.Sprintf("album=%s", station.Name),
			fmt.Sprintf("title=%s", station.Name),
		}
		if shouldEmbedCover() && station.CoverPath != "" {
			tags = append(tags, fmt.Sprintf("cover=%s", station.CoverPath))
		}
		tagsString := strings.Join(tags, ":")
		cmd := exec.Command("MP4Box", "-itags", tagsString, trackPath)
		if err := cmd.Run(); err != nil {
			logger.Errorf("Embed failed: %v\n", err)
		}
		counter.Success++
		okDict[station.ID] = append(okDict[station.ID], 1)
		if covPath != "" && !shouldKeepCoverFile() {
			if err := os.Remove(covPath); err != nil {
				logger.Errorf("Error deleting cover file: %s", covPath)
			}
		}
		return nil
	}

	for i := range station.Tracks {
		station.Tracks[i].CoverPath = covPath
		station.Tracks[i].SaveDir = playlistFolderPath
		station.Tracks[i].Codec = Codec
	}

	trackTotal := len(station.Tracks)
	arr := make([]int, trackTotal)
	for i := 0; i < trackTotal; i++ {
		arr[i] = i + 1
	}
	var selected []int

	if true {
		selected = arr
	}

	mu.Lock()
	taskTotal += len(selected)
	mu.Unlock()
	updateTotalProgress()

	var wg sync.WaitGroup
	sem := make(chan struct{}, Config.Download.Threads)

	for i := range station.Tracks {
		i++
		if isInArray(selected, i) {
			wg.Add(1)
			go func(t *task.Track) {
				defer wg.Done()
				sem <- struct{}{}
				ripTrack(t, token, mediaUserToken, sem)
			}(&station.Tracks[i-1])
		}
	}
	wg.Wait()
	if covPath != "" && !shouldKeepCoverFile() {
		if err := os.Remove(covPath); err != nil {
			logger.Errorf("Error deleting cover file: %s", covPath)
		}
	}
	return nil
}

func ripAlbum(albumId string, token string, storefront string, mediaUserToken string, urlArg_i string) error {
	album := task.NewAlbum(storefront, albumId)
	err := album.GetResp(token, Config.Auth.Language)
	if err != nil {
		logger.Error("Failed to get album response.")
		return err
	}
	meta := album.Resp
	if debug_mode {
		logger.Info(meta.Data[0].Attributes.ArtistName)
		logger.Info(meta.Data[0].Attributes.Name)

		for trackNum, track := range meta.Data[0].Relationships.Tracks.Data {
			trackNum++
			logger.Infof("\nTrack %d of %d:\n", trackNum, len(meta.Data[0].Relationships.Tracks.Data))
			logger.Infof("%02d. %s\n", trackNum, track.Attributes.Name)

			manifest, err := ampapi.GetSongResp(storefront, track.ID, album.Language, token)
			if err != nil {
				logger.Errorf("Failed to get manifest for track %d: %v\n", trackNum, err)
				continue
			}

			var m3u8Url string
			if manifest.Data[0].Attributes.ExtendedAssetUrls.EnhancedHls != "" {
				m3u8Url = manifest.Data[0].Attributes.ExtendedAssetUrls.EnhancedHls
			}
			needCheck := false
			if Config.Download.M3U8.Mode == "all" {
				needCheck = true
			} else if Config.Download.M3U8.Mode == "hires" && contains(track.Attributes.AudioTraits, "hi-res-lossless") {
				needCheck = true
			}
			if needCheck {
				fullM3u8Url, err := checkM3u8(track.ID, "song")
				if err == nil && strings.HasSuffix(fullM3u8Url, ".m3u8") {
					m3u8Url = fullM3u8Url
				} else {
					logger.Error("Failed to get best quality m3u8 from device m3u8 port, will use m3u8 from Web API")
				}
			}

			_, _, err = extractMedia(m3u8Url, true)
			if err != nil {
				logger.Errorf("Failed to extract quality info for track %d: %v\n", trackNum, err)
				continue
			}
		}
		return nil
	}
	var Codec string
	if dl_atmos {
		Codec = "ATMOS"
	} else if dl_aac {
		Codec = "AAC"
	} else {
		Codec = "ALAC"
	}
	album.Codec = Codec
	var singerFoldername string
	if Config.Naming.Artist != "" {
		if len(meta.Data[0].Relationships.Artists.Data) > 0 {
			singerFoldername = strings.NewReplacer(
				"{UrlArtistName}", LimitString(meta.Data[0].Attributes.ArtistName),
				"{ArtistName}", LimitString(meta.Data[0].Attributes.ArtistName),
				"{ArtistId}", meta.Data[0].Relationships.Artists.Data[0].ID,
			).Replace(Config.Naming.Artist)
		} else {
			singerFoldername = strings.NewReplacer(
				"{UrlArtistName}", LimitString(meta.Data[0].Attributes.ArtistName),
				"{ArtistName}", LimitString(meta.Data[0].Attributes.ArtistName),
				"{ArtistId}", "",
			).Replace(Config.Naming.Artist)
		}
		if strings.HasSuffix(singerFoldername, ".") {
			singerFoldername = strings.ReplaceAll(singerFoldername, ".", "")
		}
		singerFoldername = strings.TrimSpace(singerFoldername)
		logger.Info(singerFoldername)
	}
	singerFolder := filepath.Join(Config.Paths.Alac, forbiddenNames.ReplaceAllString(singerFoldername, "_"))
	if dl_atmos {
		singerFolder = filepath.Join(Config.Paths.Atmos, forbiddenNames.ReplaceAllString(singerFoldername, "_"))
	}
	if dl_aac {
		singerFolder = filepath.Join(Config.Paths.Aac, forbiddenNames.ReplaceAllString(singerFoldername, "_"))
	}
	os.MkdirAll(singerFolder, os.ModePerm)
	album.SaveDir = singerFolder
	var Quality string
	if strings.Contains(Config.Naming.Album, "Quality") {
		if dl_atmos {
			Quality = fmt.Sprintf("%dKbps", Config.Download.Codec.AtmosMax-2000)
		} else if dl_aac && Config.Download.Codec.AacType == "aac-lc" {
			Quality = "256Kbps"
		} else {
			manifest1, err := ampapi.GetSongResp(storefront, meta.Data[0].Relationships.Tracks.Data[0].ID, album.Language, token)
			if err != nil {
				logger.Errorf("Failed to get manifest.\n%v", err)
			} else {
				if manifest1.Data[0].Attributes.ExtendedAssetUrls.EnhancedHls == "" {
					Codec = "AAC"
					Quality = "256Kbps"
				} else {
					needCheck := false

					if Config.Download.M3U8.Mode == "all" {
						needCheck = true
					} else if Config.Download.M3U8.Mode == "hires" && contains(meta.Data[0].Relationships.Tracks.Data[0].Attributes.AudioTraits, "hi-res-lossless") {
						needCheck = true
					}
					var EnhancedHls_m3u8 string
					if needCheck {
						EnhancedHls_m3u8, _ = checkM3u8(meta.Data[0].Relationships.Tracks.Data[0].ID, "album")
						if strings.HasSuffix(EnhancedHls_m3u8, ".m3u8") {
							manifest1.Data[0].Attributes.ExtendedAssetUrls.EnhancedHls = EnhancedHls_m3u8
						}
					}
					_, Quality, err = extractMedia(manifest1.Data[0].Attributes.ExtendedAssetUrls.EnhancedHls, true)
					if err != nil {
						logger.Errorf("Failed to extract quality from manifest.\n%v", err)
					}
				}
			}
		}
	}
	stringsToJoin := []string{}
	if meta.Data[0].Attributes.IsAppleDigitalMaster || meta.Data[0].Attributes.IsMasteredForItunes {
		if Config.Naming.AppleMasterChoice != "" {
			stringsToJoin = append(stringsToJoin, Config.Naming.AppleMasterChoice)
		}
	}
	if meta.Data[0].Attributes.ContentRating == "explicit" {
		if Config.Naming.ExplicitChoice != "" {
			stringsToJoin = append(stringsToJoin, Config.Naming.ExplicitChoice)
		}
	}
	if meta.Data[0].Attributes.ContentRating == "clean" {
		if Config.Naming.CleanChoice != "" {
			stringsToJoin = append(stringsToJoin, Config.Naming.CleanChoice)
		}
	}
	Tag_string := strings.Join(stringsToJoin, " ")
	var albumFolderName string
	albumFolderName = strings.NewReplacer(
		"{ReleaseDate}", meta.Data[0].Attributes.ReleaseDate,
		"{ReleaseYear}", meta.Data[0].Attributes.ReleaseDate[:4],
		"{ArtistName}", LimitString(meta.Data[0].Attributes.ArtistName),
		"{AlbumName}", LimitString(meta.Data[0].Attributes.Name),
		"{UPC}", meta.Data[0].Attributes.Upc,
		"{RecordLabel}", meta.Data[0].Attributes.RecordLabel,
		"{Copyright}", meta.Data[0].Attributes.Copyright,
		"{AlbumId}", albumId,
		"{Quality}", Quality,
		"{Codec}", Codec,
		"{Tag}", Tag_string,
	).Replace(Config.Naming.Album)

	if strings.HasSuffix(albumFolderName, ".") {
		albumFolderName = strings.ReplaceAll(albumFolderName, ".", "")
	}
	albumFolderName = strings.TrimSpace(albumFolderName)
	albumFolderPath := filepath.Join(singerFolder, forbiddenNames.ReplaceAllString(albumFolderName, "_"))
	os.MkdirAll(albumFolderPath, os.ModePerm)
	album.SaveName = albumFolderName
	logger.Info(albumFolderName)
	if !coverDisabled && Config.Artwork.SaveArtistCover && len(meta.Data[0].Relationships.Artists.Data) > 0 {
		if meta.Data[0].Relationships.Artists.Data[0].Attributes.Artwork.Url != "" {
			_, err = writeCover(singerFolder, coverBaseName("folder"), meta.Data[0].Relationships.Artists.Data[0].Attributes.Artwork.Url)
			if err != nil {
				logger.Error("Failed to write artist cover.")
			}
		}
	}
	var covPath string
	if shouldWriteCoverFile() {
		covPath, err = writeCover(albumFolderPath, coverBaseName("cover"), meta.Data[0].Attributes.Artwork.URL)
		if err != nil {
			logger.Error("Failed to write cover.")
		}
	}
	if Config.Artwork.SaveAnimated && meta.Data[0].Attributes.EditorialVideo.MotionDetailSquare.Video != "" {
		logger.Info("Found Animation Artwork.")

		motionvideoUrlSquare, err := extractVideo(meta.Data[0].Attributes.EditorialVideo.MotionDetailSquare.Video)
		if err != nil {
			logger.Errorf("no motion video square.\n%v", err)
		} else {
			exists, err := fileExists(filepath.Join(albumFolderPath, "square_animated_artwork.mp4"))
			if err != nil {
				logger.Error("Failed to check if animated artwork square exists.")
			}
			if exists {
				logger.Info("Animated artwork square already exists locally.")
			} else {
				logger.Info("Animation Artwork Square Downloading...")
				cmd := exec.Command("ffmpeg", "-loglevel", "quiet", "-y", "-i", motionvideoUrlSquare, "-c", "copy", filepath.Join(albumFolderPath, "square_animated_artwork.mp4"))
				if err := cmd.Run(); err != nil {
					logger.Errorf("animated artwork square dl err: %v\n", err)
				} else {
					logger.Info("Animation Artwork Square Downloaded")
				}
			}
		}

		if Config.Artwork.EmbyAnimated {
			cmd3 := exec.Command("ffmpeg", "-i", filepath.Join(albumFolderPath, "square_animated_artwork.mp4"), "-vf", "scale=440:-1", "-r", "24", "-f", "gif", filepath.Join(albumFolderPath, "folder.jpg"))
			if err := cmd3.Run(); err != nil {
				logger.Errorf("animated artwork square to gif err: %v\n", err)
			}
		}

		motionvideoUrlTall, err := extractVideo(meta.Data[0].Attributes.EditorialVideo.MotionDetailTall.Video)
		if err != nil {
			logger.Errorf("no motion video tall.\n%v", err)
		} else {
			exists, err := fileExists(filepath.Join(albumFolderPath, "tall_animated_artwork.mp4"))
			if err != nil {
				logger.Error("Failed to check if animated artwork tall exists.")
			}
			if exists {
				logger.Info("Animated artwork tall already exists locally.")
			} else {
				logger.Info("Animation Artwork Tall Downloading...")
				cmd := exec.Command("ffmpeg", "-loglevel", "quiet", "-y", "-i", motionvideoUrlTall, "-c", "copy", filepath.Join(albumFolderPath, "tall_animated_artwork.mp4"))
				if err := cmd.Run(); err != nil {
					logger.Errorf("animated artwork tall dl err: %v\n", err)
				} else {
					logger.Info("Animation Artwork Tall Downloaded")
				}
			}
		}
	}
	for i := range album.Tracks {
		album.Tracks[i].CoverPath = covPath
		album.Tracks[i].SaveDir = albumFolderPath
		album.Tracks[i].Codec = Codec
	}
	trackTotal := len(meta.Data[0].Relationships.Tracks.Data)
	arr := make([]int, trackTotal)
	for i := 0; i < trackTotal; i++ {
		arr[i] = i + 1
	}

	if dl_song {
		if urlArg_i == "" {
		} else {
			for i := range album.Tracks {
				if urlArg_i == album.Tracks[i].ID {
					mu.Lock()
					taskTotal++
					mu.Unlock()
					updateTotalProgress()
					tui.AddTask(album.Tracks[i].ID, album.Tracks[i].Name)

					// Just run synchronously here since it's a single song request in album context
					sem := make(chan struct{}, 1)
					sem <- struct{}{}
					ripTrack(&album.Tracks[i], token, mediaUserToken, sem)
					return nil
				}
			}
		}
		return nil
	}
	var selected []int
	if !dl_select {
		selected = arr
	} else {
		selected = album.ShowSelect()
	}

	mu.Lock()
	taskTotal += len(selected)
	mu.Unlock()
	updateTotalProgress()

	var wg sync.WaitGroup
	sem := make(chan struct{}, Config.Download.Threads)

	for i := range album.Tracks {
		i++
		if checkOkDict(albumId, i) {
			incrementTotal()
			incrementSuccess()
			continue
		}
		if isInArray(selected, i) {
			wg.Add(1)
			go func(t *task.Track) {
				defer wg.Done()

				// Acquire download slot
				sem <- struct{}{}

				// This function will handle the entire flow, including acquiring/releasing semaphores
				ripTrack(t, token, mediaUserToken, sem)
			}(&album.Tracks[i-1])
		}
	}
	wg.Wait()
	if covPath != "" && !shouldKeepCoverFile() {
		if err := os.Remove(covPath); err != nil {
			logger.Errorf("Error deleting cover file: %s", covPath)
		}
	}
	return nil

}
func ripPlaylist(playlistId string, token string, storefront string, mediaUserToken string) error {
	playlist := task.NewPlaylist(storefront, playlistId)
	err := playlist.GetResp(token, Config.Auth.Language)
	if err != nil {
		logger.Error("Failed to get playlist response.")
		return err
	}
	meta := playlist.Resp
	if debug_mode {
		logger.Info(meta.Data[0].Attributes.ArtistName)
		logger.Info(meta.Data[0].Attributes.Name)

		for trackNum, track := range meta.Data[0].Relationships.Tracks.Data {
			trackNum++
			logger.Infof("\nTrack %d of %d:\n", trackNum, len(meta.Data[0].Relationships.Tracks.Data))
			logger.Infof("%02d. %s\n", trackNum, track.Attributes.Name)

			manifest, err := ampapi.GetSongResp(storefront, track.ID, playlist.Language, token)
			if err != nil {
				logger.Errorf("Failed to get manifest for track %d: %v\n", trackNum, err)
				continue
			}

			var m3u8Url string
			if manifest.Data[0].Attributes.ExtendedAssetUrls.EnhancedHls != "" {
				m3u8Url = manifest.Data[0].Attributes.ExtendedAssetUrls.EnhancedHls
			}
			needCheck := false
			if Config.Download.M3U8.Mode == "all" {
				needCheck = true
			} else if Config.Download.M3U8.Mode == "hires" && contains(track.Attributes.AudioTraits, "hi-res-lossless") {
				needCheck = true
			}
			if needCheck {
				fullM3u8Url, err := checkM3u8(track.ID, "song")
				if err == nil && strings.HasSuffix(fullM3u8Url, ".m3u8") {
					m3u8Url = fullM3u8Url
				} else {
					logger.Error("Failed to get best quality m3u8 from device m3u8 port, will use m3u8 from Web API")
				}
			}

			_, _, err = extractMedia(m3u8Url, true)
			if err != nil {
				logger.Errorf("Failed to extract quality info for track %d: %v\n", trackNum, err)
				continue
			}
		}
		return nil
	}
	var Codec string
	if dl_atmos {
		Codec = "ATMOS"
	} else if dl_aac {
		Codec = "AAC"
	} else {
		Codec = "ALAC"
	}
	playlist.Codec = Codec
	var singerFoldername string
	if Config.Naming.Artist != "" {
		singerFoldername = strings.NewReplacer(
			"{ArtistName}", "Apple Music",
			"{ArtistId}", "",
			"{UrlArtistName}", "Apple Music",
		).Replace(Config.Naming.Artist)
		if strings.HasSuffix(singerFoldername, ".") {
			singerFoldername = strings.ReplaceAll(singerFoldername, ".", "")
		}
		singerFoldername = strings.TrimSpace(singerFoldername)
		logger.Info(singerFoldername)
	}
	singerFolder := filepath.Join(Config.Paths.Alac, forbiddenNames.ReplaceAllString(singerFoldername, "_"))
	if dl_atmos {
		singerFolder = filepath.Join(Config.Paths.Atmos, forbiddenNames.ReplaceAllString(singerFoldername, "_"))
	}
	if dl_aac {
		singerFolder = filepath.Join(Config.Paths.Aac, forbiddenNames.ReplaceAllString(singerFoldername, "_"))
	}
	os.MkdirAll(singerFolder, os.ModePerm)
	playlist.SaveDir = singerFolder

	var Quality string
	if strings.Contains(Config.Naming.Album, "Quality") {
		if dl_atmos {
			Quality = fmt.Sprintf("%dKbps", Config.Download.Codec.AtmosMax-2000)
		} else if dl_aac && Config.Download.Codec.AacType == "aac-lc" {
			Quality = "256Kbps"
		} else {
			manifest1, err := ampapi.GetSongResp(storefront, meta.Data[0].Relationships.Tracks.Data[0].ID, playlist.Language, token)
			if err != nil {
				logger.Errorf("Failed to get manifest.\n%v", err)
			} else {
				if manifest1.Data[0].Attributes.ExtendedAssetUrls.EnhancedHls == "" {
					Codec = "AAC"
					Quality = "256Kbps"
				} else {
					needCheck := false

					if Config.Download.M3U8.Mode == "all" {
						needCheck = true
					} else if Config.Download.M3U8.Mode == "hires" && contains(meta.Data[0].Relationships.Tracks.Data[0].Attributes.AudioTraits, "hi-res-lossless") {
						needCheck = true
					}
					var EnhancedHls_m3u8 string
					if needCheck {
						EnhancedHls_m3u8, _ = checkM3u8(meta.Data[0].Relationships.Tracks.Data[0].ID, "album")
						if strings.HasSuffix(EnhancedHls_m3u8, ".m3u8") {
							manifest1.Data[0].Attributes.ExtendedAssetUrls.EnhancedHls = EnhancedHls_m3u8
						}
					}
					_, Quality, err = extractMedia(manifest1.Data[0].Attributes.ExtendedAssetUrls.EnhancedHls, true)
					if err != nil {
						logger.Errorf("Failed to extract quality from manifest.\n%v", err)
					}
				}
			}
		}
	}
	stringsToJoin := []string{}
	if meta.Data[0].Attributes.IsAppleDigitalMaster || meta.Data[0].Attributes.IsMasteredForItunes {
		if Config.Naming.AppleMasterChoice != "" {
			stringsToJoin = append(stringsToJoin, Config.Naming.AppleMasterChoice)
		}
	}
	if meta.Data[0].Attributes.ContentRating == "explicit" {
		if Config.Naming.ExplicitChoice != "" {
			stringsToJoin = append(stringsToJoin, Config.Naming.ExplicitChoice)
		}
	}
	if meta.Data[0].Attributes.ContentRating == "clean" {
		if Config.Naming.CleanChoice != "" {
			stringsToJoin = append(stringsToJoin, Config.Naming.CleanChoice)
		}
	}
	Tag_string := strings.Join(stringsToJoin, " ")
	playlistFolder := strings.NewReplacer(
		"{ArtistName}", "Apple Music",
		"{PlaylistName}", LimitString(meta.Data[0].Attributes.Name),
		"{PlaylistId}", playlistId,
		"{Quality}", Quality,
		"{Codec}", Codec,
		"{Tag}", Tag_string,
	).Replace(Config.Naming.Playlist)
	if strings.HasSuffix(playlistFolder, ".") {
		playlistFolder = strings.ReplaceAll(playlistFolder, ".", "")
	}
	playlistFolder = strings.TrimSpace(playlistFolder)
	playlistFolderPath := filepath.Join(singerFolder, forbiddenNames.ReplaceAllString(playlistFolder, "_"))
	os.MkdirAll(playlistFolderPath, os.ModePerm)
	playlist.SaveName = playlistFolder
	logger.Info(playlistFolder)
	var covPath string
	if shouldWriteCoverFile() {
		covPath, err = writeCover(playlistFolderPath, coverBaseName("cover"), meta.Data[0].Attributes.Artwork.URL)
		if err != nil {
			logger.Error("Failed to write cover.")
		}
	}

	for i := range playlist.Tracks {
		playlist.Tracks[i].CoverPath = covPath
		playlist.Tracks[i].SaveDir = playlistFolderPath
		playlist.Tracks[i].Codec = Codec
	}

	if Config.Artwork.SaveAnimated && meta.Data[0].Attributes.EditorialVideo.MotionDetailSquare.Video != "" {
		logger.Info("Found Animation Artwork.")

		motionvideoUrlSquare, err := extractVideo(meta.Data[0].Attributes.EditorialVideo.MotionDetailSquare.Video)
		if err != nil {
			logger.Errorf("no motion video square.\n%v", err)
		} else {
			exists, err := fileExists(filepath.Join(playlistFolderPath, "square_animated_artwork.mp4"))
			if err != nil {
				logger.Error("Failed to check if animated artwork square exists.")
			}
			if exists {
				logger.Info("Animated artwork square already exists locally.")
			} else {
				logger.Info("Animation Artwork Square Downloading...")
				cmd := exec.Command("ffmpeg", "-loglevel", "quiet", "-y", "-i", motionvideoUrlSquare, "-c", "copy", filepath.Join(playlistFolderPath, "square_animated_artwork.mp4"))
				if err := cmd.Run(); err != nil {
					logger.Errorf("animated artwork square dl err: %v\n", err)
				} else {
					logger.Info("Animation Artwork Square Downloaded")
				}
			}
		}

		if Config.Artwork.EmbyAnimated {
			cmd3 := exec.Command("ffmpeg", "-i", filepath.Join(playlistFolderPath, "square_animated_artwork.mp4"), "-vf", "scale=440:-1", "-r", "24", "-f", "gif", filepath.Join(playlistFolderPath, "folder.jpg"))
			if err := cmd3.Run(); err != nil {
				logger.Errorf("animated artwork square to gif err: %v\n", err)
			}
		}

		motionvideoUrlTall, err := extractVideo(meta.Data[0].Attributes.EditorialVideo.MotionDetailTall.Video)
		if err != nil {
			logger.Errorf("no motion video tall.\n%v", err)
		} else {
			exists, err := fileExists(filepath.Join(playlistFolderPath, "tall_animated_artwork.mp4"))
			if err != nil {
				logger.Error("Failed to check if animated artwork tall exists.")
			}
			if exists {
				logger.Info("Animated artwork tall already exists locally.")
			} else {
				logger.Info("Animation Artwork Tall Downloading...")
				cmd := exec.Command("ffmpeg", "-loglevel", "quiet", "-y", "-i", motionvideoUrlTall, "-c", "copy", filepath.Join(playlistFolderPath, "tall_animated_artwork.mp4"))
				if err := cmd.Run(); err != nil {
					logger.Errorf("animated artwork tall dl err: %v\n", err)
				} else {
					logger.Info("Animation Artwork Tall Downloaded")
				}
			}
		}
	}
	trackTotal := len(meta.Data[0].Relationships.Tracks.Data)
	arr := make([]int, trackTotal)
	for i := 0; i < trackTotal; i++ {
		arr[i] = i + 1
	}
	var selected []int

	if !dl_select {
		selected = arr
	} else {
		selected = playlist.ShowSelect()
	}

	mu.Lock()
	taskTotal += len(selected)
	mu.Unlock()
	updateTotalProgress()

	var wg sync.WaitGroup
	sem := make(chan struct{}, Config.Download.Threads)

	for i := range playlist.Tracks {
		i++
		if checkOkDict(playlistId, i) {
			incrementTotal()
			incrementSuccess()
			continue
		}
		if isInArray(selected, i) {
			wg.Add(1)
			go func(t *task.Track) {
				defer wg.Done()
				sem <- struct{}{}
				ripTrack(t, token, mediaUserToken, sem)
			}(&playlist.Tracks[i-1])
		}
	}
	wg.Wait()
	if covPath != "" && !shouldKeepCoverFile() {
		if err := os.Remove(covPath); err != nil {
			logger.Errorf("Error deleting cover file: %s", covPath)
		}
	}
	return nil
}

func writeMP4Tags(track *task.Track, lrc string) error {
	t := &mp4tag.MP4Tags{
		Title:      track.Resp.Attributes.Name,
		TitleSort:  track.Resp.Attributes.Name,
		Artist:     track.Resp.Attributes.ArtistName,
		ArtistSort: track.Resp.Attributes.ArtistName,
		Custom: map[string]string{
			"PERFORMER":   track.Resp.Attributes.ArtistName,
			"RELEASETIME": track.Resp.Attributes.ReleaseDate,
			"ISRC":        track.Resp.Attributes.Isrc,
			"LABEL":       "",
			"UPC":         "",
		},
		Composer:     track.Resp.Attributes.ComposerName,
		ComposerSort: track.Resp.Attributes.ComposerName,
		CustomGenre:  track.Resp.Attributes.GenreNames[0],
		Lyrics:       lrc,
		TrackNumber:  int16(track.Resp.Attributes.TrackNumber),
		DiscNumber:   int16(track.Resp.Attributes.DiscNumber),
		Album:        track.Resp.Attributes.AlbumName,
		AlbumSort:    track.Resp.Attributes.AlbumName,
	}

	if track.PreType == "albums" {
		albumID, err := strconv.ParseUint(track.PreID, 10, 32)
		if err != nil {
			return err
		}
		t.ItunesAlbumID = int32(albumID)
	}

	if len(track.Resp.Relationships.Artists.Data) > 0 {
		artistID, err := strconv.ParseUint(track.Resp.Relationships.Artists.Data[0].ID, 10, 32)
		if err != nil {
			return err
		}
		t.ItunesArtistID = int32(artistID)
	}

	if (track.PreType == "playlists" || track.PreType == "stations") && !Config.Download.Playlist.UseSongInfo {
		t.DiscNumber = 1
		t.DiscTotal = 1
		t.TrackNumber = int16(track.TaskNum)
		t.TrackTotal = int16(track.TaskTotal)
		t.Album = track.PlaylistData.Attributes.Name
		t.AlbumSort = track.PlaylistData.Attributes.Name
		t.AlbumArtist = track.PlaylistData.Attributes.ArtistName
		t.AlbumArtistSort = track.PlaylistData.Attributes.ArtistName
	} else if (track.PreType == "playlists" || track.PreType == "stations") && Config.Download.Playlist.UseSongInfo {
		t.DiscTotal = int16(track.DiscTotal)
		t.TrackTotal = int16(track.AlbumData.Attributes.TrackCount)
		t.AlbumArtist = track.AlbumData.Attributes.ArtistName
		t.AlbumArtistSort = track.AlbumData.Attributes.ArtistName
		t.Custom["UPC"] = track.AlbumData.Attributes.Upc
		t.Custom["LABEL"] = track.AlbumData.Attributes.RecordLabel
		t.Date = track.AlbumData.Attributes.ReleaseDate
		t.Copyright = track.AlbumData.Attributes.Copyright
		t.Publisher = track.AlbumData.Attributes.RecordLabel
	} else {
		t.DiscTotal = int16(track.DiscTotal)
		t.TrackTotal = int16(track.AlbumData.Attributes.TrackCount)
		t.AlbumArtist = track.AlbumData.Attributes.ArtistName
		t.AlbumArtistSort = track.AlbumData.Attributes.ArtistName
		t.Custom["UPC"] = track.AlbumData.Attributes.Upc
		t.Date = track.AlbumData.Attributes.ReleaseDate
		t.Copyright = track.AlbumData.Attributes.Copyright
		t.Publisher = track.AlbumData.Attributes.RecordLabel
	}

	if track.Resp.Attributes.ContentRating == "explicit" {
		t.ItunesAdvisory = mp4tag.ItunesAdvisoryExplicit
	} else if track.Resp.Attributes.ContentRating == "clean" {
		t.ItunesAdvisory = mp4tag.ItunesAdvisoryClean
	} else {
		t.ItunesAdvisory = mp4tag.ItunesAdvisoryNone
	}

	mp4, err := mp4tag.Open(track.SavePath)
	if err != nil {
		return err
	}
	defer mp4.Close()
	err = mp4.Write(t, []string{})
	if err != nil {
		return err
	}
	return nil
}

func incrementTotal() {
	mu.Lock()
	defer mu.Unlock()
	counter.Total++
}

func incrementSuccess() {
	mu.Lock()
	defer mu.Unlock()
	counter.Success++
	updateTotalProgress()
}

func incrementError() {
	mu.Lock()
	defer mu.Unlock()
	counter.Error++
	updateTotalProgress()
}

func incrementUnavailable() {
	mu.Lock()
	defer mu.Unlock()
	counter.Unavailable++
	updateTotalProgress()
}

func updateTotalProgress() {
	if taskTotal > 0 {
		completed := counter.Success + counter.Error + counter.Unavailable + counter.NotSong
		percent := float64(completed) / float64(taskTotal)
		tui.UpdateTotalProgress(percent)
	}
}

func addToOkDict(key string, val int) {
	mu.Lock()
	defer mu.Unlock()
	okDict[key] = append(okDict[key], val)
}

func checkOkDict(key string, val int) bool {
	mu.Lock()
	defer mu.Unlock()
	return isInArray(okDict[key], val)
}

func runDownloads(args []string, token string) {
	albumTotal := len(args)
	for {
		for albumNum, urlRaw := range args {
			logger.Infof("Queue %d of %d: ", albumNum+1, albumTotal)
			var storefront, albumId string

			if strings.Contains(urlRaw, "/music-video/") {
				logger.Info("Music Video")
				if debug_mode {
					continue
				}
				mu.Lock()
				taskTotal++
				mu.Unlock()
				updateTotalProgress()

				counter.Total++
				if len(Config.Auth.MediaUserToken) <= 50 {
					logger.Info(": meida-user-token is not set, skip MV dl")
					counter.Success++
					continue
				}
				if _, err := exec.LookPath("mp4decrypt"); err != nil {
					logger.Info(": mp4decrypt is not found, skip MV dl")
					counter.Success++
					continue
				}
				mvSaveDir := strings.NewReplacer(
					"{ArtistName}", "",
					"{UrlArtistName}", "",
					"{ArtistId}", "",
				).Replace(Config.Naming.Artist)
				if mvSaveDir != "" {
					mvSaveDir = filepath.Join(Config.Paths.Alac, forbiddenNames.ReplaceAllString(mvSaveDir, "_"))
				} else {
					mvSaveDir = Config.Paths.Alac
				}
				storefront, albumId = checkUrlMv(urlRaw)
				tui.AddTask(albumId, "Music Video")
				cb := func(p float64, msg string, speed float64) {
					tui.UpdateTask(albumId, p, msg, "downloading", speed)
				}
				err := mvDownloader(albumId, mvSaveDir, token, storefront, Config.Auth.MediaUserToken, nil, cb)
				tui.UpdateTask(albumId, 1.0, "Done", "done", 0)
				if err != nil {
					logger.Errorf("\u26A0 Failed to dl MV: %v", err)
					counter.Error++
					continue
				}
				counter.Success++
				continue
			}
			if strings.Contains(urlRaw, "/song/") {
				logger.Info("Song->")
				storefront, songId := checkUrlSong(urlRaw)
				if storefront == "" || songId == "" {
					logger.Error("Invalid song URL format.")
					continue
				}
				err := ripSong(songId, token, storefront, Config.Auth.MediaUserToken)
				if err != nil {
					logger.Error("Failed to rip song:", err)
				}
				continue
			}
			parse, err := url.Parse(urlRaw)
			if err != nil {
				logger.Fatalf("Invalid URL: %v", err)
			}
			var urlArg_i = parse.Query().Get("i")

			if strings.Contains(urlRaw, "/album/") {
				logger.Info("Album")
				storefront, albumId = checkUrl(urlRaw)
				err := ripAlbum(albumId, token, storefront, Config.Auth.MediaUserToken, urlArg_i)
				if err != nil {
					logger.Errorf("Failed to rip album: %v", err)
				}
			} else if strings.Contains(urlRaw, "/playlist/") {
				logger.Info("Playlist")
				storefront, albumId = checkUrlPlaylist(urlRaw)
				err := ripPlaylist(albumId, token, storefront, Config.Auth.MediaUserToken)
				if err != nil {
					logger.Errorf("Failed to rip playlist: %v", err)
				}
			} else if strings.Contains(urlRaw, "/station/") {
				logger.Info("Station")
				storefront, albumId = checkUrlStation(urlRaw)
				if len(Config.Auth.MediaUserToken) <= 50 {
					logger.Info(": meida-user-token is not set, skip station dl")
					continue
				}
				err := ripStation(albumId, token, storefront, Config.Auth.MediaUserToken)
				if err != nil {
					logger.Errorf("Failed to rip station: %v", err)
				}
			} else {
				logger.Error("Invalid type")
			}
		}
		logger.Infof("=======  [\u2714 ] Completed: %d/%d  |  [\u26A0 ] Warnings: %d  |  [\u2716 ] Errors: %d  =======\n", counter.Success, counter.Total, counter.Unavailable+counter.NotSong, counter.Error)
		if counter.Error == 0 {
			break
		}
		logger.Info("Error detected, press Enter to try again...")
		fmt.Scanln()
		logger.Info("Start trying again...")
		counter = structs.Counter{}
	}
}

func mvDownloader(adamID string, saveDir string, token string, storefront string, mediaUserToken string, track *task.Track, cb structs.ProgressCallback) error {
	MVInfo, err := ampapi.GetMusicVideoResp(storefront, adamID, Config.Auth.Language, token)
	if err != nil {
		logger.Errorf("Failed to get MV manifest: %v", err)
		return nil
	}

	if strings.HasSuffix(saveDir, ".") {
		saveDir = strings.ReplaceAll(saveDir, ".", "")
	}
	saveDir = strings.TrimSpace(saveDir)

	vidPath := filepath.Join(saveDir, fmt.Sprintf("%s_vid.mp4", adamID))
	audPath := filepath.Join(saveDir, fmt.Sprintf("%s_aud.mp4", adamID))
	mvSaveName := fmt.Sprintf("%s (%s)", MVInfo.Data[0].Attributes.Name, adamID)
	if track != nil {
		mvSaveName = fmt.Sprintf("%02d. %s", track.TaskNum, MVInfo.Data[0].Attributes.Name)
	}

	mvOutPath := filepath.Join(saveDir, fmt.Sprintf("%s.mp4", forbiddenNames.ReplaceAllString(mvSaveName, "_")))

	logger.Info(MVInfo.Data[0].Attributes.Name)

	exists, _ := fileExists(mvOutPath)
	if exists {
		logger.Info("MV already exists locally.")
		return nil
	}

	mvm3u8url, _, _, _ := runv3.GetWebplayback(adamID, token, mediaUserToken, true)
	if mvm3u8url == "" {
		return errors.New("media-user-token may wrong or expired")
	}

	os.MkdirAll(saveDir, os.ModePerm)
	videom3u8url, _ := extractVideo(mvm3u8url)
	videokeyAndUrls, _ := runv3.Run(adamID, videom3u8url, token, mediaUserToken, true, "", func(p float64, msg string, speed float64) {
		if cb != nil {
			cb(p, "Downloading MV Video", speed)
		}
	})
	_ = runv3.ExtMvData(videokeyAndUrls, vidPath)
	defer os.Remove(vidPath)
	audiom3u8url, _ := extractMvAudio(mvm3u8url)
	audiokeyAndUrls, _ := runv3.Run(adamID, audiom3u8url, token, mediaUserToken, true, "", func(p float64, msg string, speed float64) {
		if cb != nil {
			cb(p, "Downloading MV Audio", speed)
		}
	})
	_ = runv3.ExtMvData(audiokeyAndUrls, audPath)
	defer os.Remove(audPath)

	tags := []string{
		"tool=",
		fmt.Sprintf("artist=%s", MVInfo.Data[0].Attributes.ArtistName),
		fmt.Sprintf("title=%s", MVInfo.Data[0].Attributes.Name),
		fmt.Sprintf("genre=%s", MVInfo.Data[0].Attributes.GenreNames[0]),
		fmt.Sprintf("created=%s", MVInfo.Data[0].Attributes.ReleaseDate),
		fmt.Sprintf("ISRC=%s", MVInfo.Data[0].Attributes.Isrc),
	}

	if MVInfo.Data[0].Attributes.ContentRating == "explicit" {
		tags = append(tags, "rating=1")
	} else if MVInfo.Data[0].Attributes.ContentRating == "clean" {
		tags = append(tags, "rating=2")
	} else {
		tags = append(tags, "rating=0")
	}

	if track != nil {
		if track.PreType == "playlists" && !Config.Download.Playlist.UseSongInfo {
			tags = append(tags, "disk=1/1")
			tags = append(tags, fmt.Sprintf("album=%s", track.PlaylistData.Attributes.Name))
			tags = append(tags, fmt.Sprintf("track=%d", track.TaskNum))
			tags = append(tags, fmt.Sprintf("tracknum=%d/%d", track.TaskNum, track.TaskTotal))
			tags = append(tags, fmt.Sprintf("album_artist=%s", track.PlaylistData.Attributes.ArtistName))
			tags = append(tags, fmt.Sprintf("performer=%s", track.Resp.Attributes.ArtistName))
		} else if track.PreType == "playlists" && Config.Download.Playlist.UseSongInfo {
			tags = append(tags, fmt.Sprintf("album=%s", track.AlbumData.Attributes.Name))
			tags = append(tags, fmt.Sprintf("disk=%d/%d", track.Resp.Attributes.DiscNumber, track.DiscTotal))
			tags = append(tags, fmt.Sprintf("track=%d", track.Resp.Attributes.TrackNumber))
			tags = append(tags, fmt.Sprintf("tracknum=%d/%d", track.Resp.Attributes.TrackNumber, track.AlbumData.Attributes.TrackCount))
			tags = append(tags, fmt.Sprintf("album_artist=%s", track.AlbumData.Attributes.ArtistName))
			tags = append(tags, fmt.Sprintf("performer=%s", track.Resp.Attributes.ArtistName))
			tags = append(tags, fmt.Sprintf("copyright=%s", track.AlbumData.Attributes.Copyright))
			tags = append(tags, fmt.Sprintf("UPC=%s", track.AlbumData.Attributes.Upc))
		} else {
			tags = append(tags, fmt.Sprintf("album=%s", track.AlbumData.Attributes.Name))
			tags = append(tags, fmt.Sprintf("disk=%d/%d", track.Resp.Attributes.DiscNumber, track.DiscTotal))
			tags = append(tags, fmt.Sprintf("track=%d", track.Resp.Attributes.TrackNumber))
			tags = append(tags, fmt.Sprintf("tracknum=%d/%d", track.Resp.Attributes.TrackNumber, track.AlbumData.Attributes.TrackCount))
			tags = append(tags, fmt.Sprintf("album_artist=%s", track.AlbumData.Attributes.ArtistName))
			tags = append(tags, fmt.Sprintf("performer=%s", track.Resp.Attributes.ArtistName))
			tags = append(tags, fmt.Sprintf("copyright=%s", track.AlbumData.Attributes.Copyright))
			tags = append(tags, fmt.Sprintf("UPC=%s", track.AlbumData.Attributes.Upc))
		}
	} else {
		tags = append(tags, fmt.Sprintf("album=%s", MVInfo.Data[0].Attributes.AlbumName))
		tags = append(tags, fmt.Sprintf("disk=%d", MVInfo.Data[0].Attributes.DiscNumber))
		tags = append(tags, fmt.Sprintf("track=%d", MVInfo.Data[0].Attributes.TrackNumber))
		tags = append(tags, fmt.Sprintf("tracknum=%d", MVInfo.Data[0].Attributes.TrackNumber))
		tags = append(tags, fmt.Sprintf("performer=%s", MVInfo.Data[0].Attributes.ArtistName))
	}

	var covPath string
	if shouldWriteCoverFile() {
		thumbURL := MVInfo.Data[0].Attributes.Artwork.URL
		baseThumbName := forbiddenNames.ReplaceAllString(mvSaveName, "_") + "_thumbnail"
		if coverFile && coverName != "" {
			baseThumbName = coverBaseName(baseThumbName)
		}
		covPath, err = writeCover(saveDir, baseThumbName, thumbURL)
		if err != nil {
			logger.Errorf("Failed to save MV thumbnail: %v", err)
		} else if shouldEmbedCover() {
			tags = append(tags, fmt.Sprintf("cover=%s", covPath))
		}
	}
	if covPath != "" && !shouldKeepCoverFile() {
		defer os.Remove(covPath)
	}

	tagsString := strings.Join(tags, ":")
	muxCmd := exec.Command("MP4Box", "-itags", tagsString, "-quiet", "-add", vidPath, "-add", audPath, "-keep-utc", "-new", mvOutPath)
	logger.Infof("MV Remuxing...")
	if err := muxCmd.Run(); err != nil {
		logger.Errorf("MV mux failed: %v\n", err)
		return err
	}
	logger.Info("MV Remuxed.")
	return nil
}

func extractMvAudio(c string) (string, error) {
	MediaUrl, err := url.Parse(c)
	if err != nil {
		return "", err
	}

	resp, err := http.Get(c)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", errors.New(resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	audioString := string(body)
	from, listType, err := m3u8.DecodeFrom(strings.NewReader(audioString), true)
	if err != nil || listType != m3u8.MASTER {
		return "", errors.New("m3u8 not of media type")
	}

	audio := from.(*m3u8.MasterPlaylist)

	var audioPriority = []string{"audio-atmos", "audio-ac3", "audio-stereo-256"}
	if Config.Download.MV.AudioType == "ac3" {
		audioPriority = []string{"audio-ac3", "audio-stereo-256"}
	} else if Config.Download.MV.AudioType == "aac" {
		audioPriority = []string{"audio-stereo-256"}
	}

	re := regexp.MustCompile(`_gr(\d+)_`)

	type AudioStream struct {
		URL     string
		Rank    int
		GroupID string
	}
	var audioStreams []AudioStream

	for _, variant := range audio.Variants {
		for _, audiov := range variant.Alternatives {
			if audiov.URI != "" {
				for _, priority := range audioPriority {
					if audiov.GroupId == priority {
						matches := re.FindStringSubmatch(audiov.URI)
						if len(matches) == 2 {
							var rank int
							fmt.Sscanf(matches[1], "%d", &rank)
							streamUrl, _ := MediaUrl.Parse(audiov.URI)
							audioStreams = append(audioStreams, AudioStream{
								URL:     streamUrl.String(),
								Rank:    rank,
								GroupID: audiov.GroupId,
							})
						}
					}
				}
			}
		}
	}

	if len(audioStreams) == 0 {
		return "", errors.New("no suitable audio stream found")
	}

	sort.Slice(audioStreams, func(i, j int) bool {
		return audioStreams[i].Rank > audioStreams[j].Rank
	})
	logger.Info("Audio: " + audioStreams[0].GroupID)
	return audioStreams[0].URL, nil
}

func checkM3u8(b string, f string) (string, error) {
	var EnhancedHls string
	if Config.Download.M3U8.GetFromDevice {
		adamID := b
		conn, err := net.Dial("tcp", Config.Download.M3U8.GetPort)
		if err != nil {
			logger.Errorf("Error connecting to device: %v", err)
			return "none", err
		}
		defer conn.Close()
		if f == "song" {
			logger.Info("Connected to device")
		}

		adamIDBuffer := []byte(adamID)
		lengthBuffer := []byte{byte(len(adamIDBuffer))}

		_, err = conn.Write(lengthBuffer)
		if err != nil {
			logger.Errorf("Error writing length to device: %v", err)
			return "none", err
		}

		_, err = conn.Write(adamIDBuffer)
		if err != nil {
			logger.Errorf("Error writing adamID to device: %v", err)
			return "none", err
		}

		response, err := bufio.NewReader(conn).ReadBytes('\n')
		if err != nil {
			logger.Errorf("Error reading response from device: %v", err)
			return "none", err
		}

		response = bytes.TrimSpace(response)
		if len(response) > 0 {
			if f == "song" {
				logger.Infof("Received URL: %s", string(response))
			}
			EnhancedHls = string(response)
		} else {
			logger.Warn("Received an empty response")
		}
	}
	return EnhancedHls, nil
}

func formatAvailability(available bool, quality string) string {
	if !available {
		return "Not Available"
	}
	return quality
}

func extractMedia(b string, more_mode bool) (string, string, error) {
	masterUrl, err := url.Parse(b)
	if err != nil {
		return "", "", err
	}
	resp, err := http.Get(b)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", errors.New(resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", err
	}
	masterString := string(body)
	from, listType, err := m3u8.DecodeFrom(strings.NewReader(masterString), true)
	if err != nil || listType != m3u8.MASTER {
		return "", "", errors.New("m3u8 not of master type")
	}
	master := from.(*m3u8.MasterPlaylist)
	var streamUrl *url.URL
	sort.Slice(master.Variants, func(i, j int) bool {
		return master.Variants[i].AverageBandwidth > master.Variants[j].AverageBandwidth
	})
	if debug_mode && more_mode {
		logger.Debug("All Available Variants:")
		var data [][]string
		for _, variant := range master.Variants {
			data = append(data, []string{variant.Codecs, variant.Audio, fmt.Sprint(variant.Bandwidth)})
		}
		table := tablewriter.NewWriter(os.Stdout)
		table.SetHeader([]string{"Codec", "Audio", "Bandwidth"})
		table.SetAutoMergeCells(true)
		table.SetRowLine(true)
		table.AppendBulk(data)
		table.Render()

		var hasAAC, hasLossless, hasHiRes, hasAtmos, hasDolbyAudio bool
		var aacQuality, losslessQuality, hiResQuality, atmosQuality, dolbyAudioQuality string

		for _, variant := range master.Variants {
			if variant.Codecs == "mp4a.40.2" { // AAC
				hasAAC = true
				split := strings.Split(variant.Audio, "-")
				if len(split) >= 3 {
					bitrate, _ := strconv.Atoi(split[2])
					currentBitrate := 0
					if aacQuality != "" {
						current := strings.Split(aacQuality, " | ")[2]
						current = strings.Split(current, " ")[0]
						currentBitrate, _ = strconv.Atoi(current)
					}
					if bitrate > currentBitrate {
						aacQuality = fmt.Sprintf("AAC | 2 Channel | %d Kbps", bitrate)
					}
				}
			} else if variant.Codecs == "ec-3" && strings.Contains(variant.Audio, "atmos") { // Dolby Atmos
				hasAtmos = true
				split := strings.Split(variant.Audio, "-")
				if len(split) > 0 {
					bitrateStr := split[len(split)-1]
					if len(bitrateStr) == 4 && bitrateStr[0] == '2' {
						bitrateStr = bitrateStr[1:]
					}
					bitrate, _ := strconv.Atoi(bitrateStr)
					currentBitrate := 0
					if atmosQuality != "" {
						current := strings.Split(strings.Split(atmosQuality, " | ")[2], " ")[0]
						currentBitrate, _ = strconv.Atoi(current)
					}
					if bitrate > currentBitrate {
						atmosQuality = fmt.Sprintf("E-AC-3 | 16 Channel | %d Kbps", bitrate)
					}
				}
			} else if variant.Codecs == "alac" { // ALAC (Lossless or Hi-Res)
				split := strings.Split(variant.Audio, "-")
				if len(split) >= 3 {
					bitDepth := split[len(split)-1]
					sampleRate := split[len(split)-2]
					sampleRateInt, _ := strconv.Atoi(sampleRate)
					if sampleRateInt > 48000 { // Hi-Res
						hasHiRes = true
						hiResQuality = fmt.Sprintf("ALAC | 2 Channel | %s-bit/%d kHz", bitDepth, sampleRateInt/1000)
					} else { // Standard Lossless
						hasLossless = true
						losslessQuality = fmt.Sprintf("ALAC | 2 Channel | %s-bit/%d kHz", bitDepth, sampleRateInt/1000)
					}
				}
			} else if variant.Codecs == "ac-3" { // Dolby Audio
				hasDolbyAudio = true
				split := strings.Split(variant.Audio, "-")
				if len(split) > 0 {
					bitrate, _ := strconv.Atoi(split[len(split)-1])
					dolbyAudioQuality = fmt.Sprintf("AC-3 |  16 Channel | %d Kbps", bitrate)
				}
			}
		}

		logger.Info("Available Audio Formats:")
		logger.Info("------------------------")
		logger.Infof("AAC             : %s\n", formatAvailability(hasAAC, aacQuality))
		logger.Infof("Lossless        : %s\n", formatAvailability(hasLossless, losslessQuality))
		logger.Infof("Hi-Res Lossless : %s\n", formatAvailability(hasHiRes, hiResQuality))
		logger.Infof("Dolby Atmos     : %s\n", formatAvailability(hasAtmos, atmosQuality))
		logger.Infof("Dolby Audio     : %s\n", formatAvailability(hasDolbyAudio, dolbyAudioQuality))
		logger.Info("------------------------")

		return "", "", nil
	}
	var Quality string
	for _, variant := range master.Variants {
		if dl_atmos {
			if variant.Codecs == "ec-3" && strings.Contains(variant.Audio, "atmos") {
				if debug_mode && !more_mode {
					logger.Infof("Debug: Found Dolby Atmos variant - %s (Bitrate: %d Kbps)\n",
						variant.Audio, variant.Bandwidth/1000)
				}
				split := strings.Split(variant.Audio, "-")
				length := len(split)
				length_int, err := strconv.Atoi(split[length-1])
				if err != nil {
					return "", "", err
				}
				if length_int <= Config.Download.Codec.AtmosMax {
					if !debug_mode && !more_mode {
						logger.Infof("%s\n", variant.Audio)
					}
					streamUrlTemp, err := masterUrl.Parse(variant.URI)
					if err != nil {
						return "", "", err
					}
					streamUrl = streamUrlTemp
					Quality = fmt.Sprintf("%s Kbps", split[len(split)-1])
					break
				}
			} else if variant.Codecs == "ac-3" { // Add Dolby Audio support
				if debug_mode && !more_mode {
					logger.Infof("Debug: Found Dolby Audio variant - %s (Bitrate: %d Kbps)\n",
						variant.Audio, variant.Bandwidth/1000)
				}
				streamUrlTemp, err := masterUrl.Parse(variant.URI)
				if err != nil {
					return "", "", err
				}
				streamUrl = streamUrlTemp
				split := strings.Split(variant.Audio, "-")
				Quality = fmt.Sprintf("%s Kbps", split[len(split)-1])
				break
			}
		} else if dl_aac {
			if variant.Codecs == "mp4a.40.2" {
				if debug_mode && !more_mode {
					logger.Infof("Debug: Found AAC variant - %s (Bitrate: %d)\n", variant.Audio, variant.Bandwidth)
				}
				aacregex := regexp.MustCompile(`audio-stereo-\d+`)
				replaced := aacregex.ReplaceAllString(variant.Audio, "aac")
				if replaced == Config.Download.Codec.AacType {
					if !debug_mode && !more_mode {
						logger.Infof("%s\n", variant.Audio)
					}
					streamUrlTemp, err := masterUrl.Parse(variant.URI)
					if err != nil {
						panic(err)
					}
					streamUrl = streamUrlTemp
					split := strings.Split(variant.Audio, "-")
					Quality = fmt.Sprintf("%s Kbps", split[2])
					break
				}
			}
		} else {
			if variant.Codecs == "alac" {
				split := strings.Split(variant.Audio, "-")
				length := len(split)
				length_int, err := strconv.Atoi(split[length-2])
				if err != nil {
					return "", "", err
				}
				if length_int <= Config.Download.Codec.AlacMax {
					if !debug_mode && !more_mode {
						logger.Infof("%s-bit / %s Hz\n", split[length-1], split[length-2])
					}
					streamUrlTemp, err := masterUrl.Parse(variant.URI)
					if err != nil {
						panic(err)
					}
					streamUrl = streamUrlTemp
					KHZ := float64(length_int) / 1000.0
					Quality = fmt.Sprintf("%sB-%.1fkHz", split[length-1], KHZ)
					break
				}
			}
		}
	}
	if streamUrl == nil {
		return "", "", errors.New("no codec found")
	}
	return streamUrl.String(), Quality, nil
}
func extractVideo(c string) (string, error) {
	MediaUrl, err := url.Parse(c)
	if err != nil {
		return "", err
	}

	resp, err := http.Get(c)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", errors.New(resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	videoString := string(body)

	from, listType, err := m3u8.DecodeFrom(strings.NewReader(videoString), true)
	if err != nil || listType != m3u8.MASTER {
		return "", errors.New("m3u8 not of media type")
	}

	video := from.(*m3u8.MasterPlaylist)

	re := regexp.MustCompile(`_(\d+)x(\d+)`)

	var streamUrl *url.URL
	sort.Slice(video.Variants, func(i, j int) bool {
		return video.Variants[i].AverageBandwidth > video.Variants[j].AverageBandwidth
	})

	maxHeight := Config.Download.MV.Max

	for _, variant := range video.Variants {
		matches := re.FindStringSubmatch(variant.URI)
		if len(matches) == 3 {
			height := matches[2]
			var h int
			_, err := fmt.Sscanf(height, "%d", &h)
			if err != nil {
				continue
			}
			if h <= maxHeight {
				streamUrl, err = MediaUrl.Parse(variant.URI)
				if err != nil {
					return "", err
				}
				logger.Info("Video: " + variant.Resolution + "-" + variant.VideoRange)
				break
			}
		}
	}

	if streamUrl == nil {
		return "", errors.New("no suitable video stream found")
	}

	return streamUrl.String(), nil
}

func ripSong(songId string, token string, storefront string, mediaUserToken string) error {
	// Get song info to find album ID
	manifest, err := ampapi.GetSongResp(storefront, songId, Config.Auth.Language, token)
	if err != nil {
		logger.Error("Failed to get song response.")
		return err
	}

	songData := manifest.Data[0]
	albumId := songData.Relationships.Albums.Data[0].ID

	// Use album approach but only download the specific song
	dl_song = true
	err = ripAlbum(albumId, token, storefront, mediaUserToken, songId)
	if err != nil {
		logger.Error("Failed to rip song:", err)
		return err
	}

	return nil
}

const defaultConfigRelPath = ".config/amdl/config.yaml"

func defaultConfigPath() string {
	if env := os.Getenv("AMDL_CONFIG"); env != "" {
		return env
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "config.yaml"
	}
	return filepath.Join(home, defaultConfigRelPath)
}

func resolveConfigPath() (string, bool) {
	path := defaultConfigPath()
	if _, err := os.Stat(path); err == nil {
		return path, true
	}
	if _, err := os.Stat("config.yaml"); err == nil {
		return "config.yaml", true
	}
	return path, false
}

func loadConfigFrom(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := yaml.Unmarshal(data, &Config); err != nil {
		return err
	}
	applyConfigDefaults()
	return nil
}

func applyConfigDefaults() {
	if len(Config.Auth.Storefront) != 2 {
		Config.Auth.Storefront = "us"
	}
	if Config.Naming.LimitMax <= 0 {
		Config.Naming.LimitMax = 200
	}
	if Config.Download.Threads <= 0 {
		Config.Download.Threads = 1
	}
	if Config.Download.MaxMemory <= 0 {
		Config.Download.MaxMemory = 256
	}
	if Config.Download.M3U8.Mode == "" {
		Config.Download.M3U8.Mode = "hires"
	}
	switch Config.Lyrics.Type {
	case "syllable":
		Config.Lyrics.Type = "syllable-lyrics"
	case "":
		Config.Lyrics.Type = "lyrics"
	}
	if Config.Lyrics.Format == "" {
		Config.Lyrics.Format = "ttml"
	}
	if !Config.Lyrics.Enable && (Config.Lyrics.Embed || Config.Lyrics.SaveFile) {
		Config.Lyrics.Enable = true
	}
}

func initConfigAt(path string) error {
	if path == "" {
		return errors.New("config path is empty")
	}
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("config already exists at %s", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	data, err := os.ReadFile("config.example.yaml")
	if err != nil {
		return fmt.Errorf("failed to read config.example.yaml: %w", err)
	}
	return os.WriteFile(path, data, 0o600)
}

func showConfig(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	fmt.Printf("%s\n", strings.TrimSpace(string(data)))
	return nil
}

func editConfig(path string) error {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}
	cmd := exec.Command(editor, path)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func shouldWriteCoverFile() bool {
	if coverDisabled {
		return false
	}
	return Config.Artwork.EmbedCover || coverFile
}

func shouldEmbedCover() bool {
	if coverDisabled {
		return false
	}
	return Config.Artwork.EmbedCover
}

func shouldKeepCoverFile() bool {
	return coverFile && !coverDisabled
}

func coverBaseName(defaultName string) string {
	if coverFile && coverName != "" {
		return coverName
	}
	return defaultName
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		return
	}

	if os.Args[1] == "help" || os.Args[1] == "-h" || os.Args[1] == "--help" {
		if len(os.Args) > 2 {
			printHelpTopic(os.Args[2])
			return
		}
		printUsage()
		return
	}

	switch os.Args[1] {
	case "download":
		handleDownload(os.Args[2:])
	case "search":
		handleSearchCmd(os.Args[2:])
	case "metadata":
		handleMetadata(os.Args[2:])
	case "config":
		handleConfig(os.Args[2:])
	case "login":
		handleLogin(os.Args[2:])
	case "debug":
		handleDebug(os.Args[2:])
	default:
		printUsage()
	}
}

func printUsage() {
	fmt.Println("Apple Music Downloader (amdl)")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  amdl <command> [arguments]")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  download    download music, albums, playlists, artists")
	fmt.Println("  search      search Apple Music")
	fmt.Println("  metadata    show metadata only (no download)")
	fmt.Println("  config      manage configuration")
	fmt.Println("  login       open config for auth setup")
	fmt.Println("  debug       show available audio qualities")
	fmt.Println()
	fmt.Println("Use \"amdl help <command>\" for more information about a command.")
}

func printHelpTopic(cmd string) {
	switch strings.ToLower(cmd) {
	case "download":
		printDownloadHelp()
	case "search":
		printSearchHelp()
	case "metadata":
		printMetadataHelp()
	case "config":
		printConfigHelp()
	case "login":
		printLoginHelp()
	case "debug":
		printDebugHelp()
	default:
		printUsage()
	}
}

func handleConfig(args []string) {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		printConfigHelp()
		return
	}
	if len(args) == 0 {
		fmt.Println("Usage: amdl config <init|show|edit>")
		return
	}
	sub := args[0]
	path := defaultConfigPath()

	switch sub {
	case "init":
		if err := initConfigAt(path); err != nil {
			fmt.Printf("Config init failed: %v\n", err)
			return
		}
		fmt.Printf("Config created at %s\n", path)
	case "show":
		cfgPath, ok := resolveConfigPath()
		if !ok {
			fmt.Printf("Config not found. Run: amdl config init (target: %s)\n", cfgPath)
			return
		}
		if err := showConfig(cfgPath); err != nil {
			fmt.Printf("Failed to read config: %v\n", err)
		}
	case "edit":
		cfgPath, ok := resolveConfigPath()
		if !ok {
			fmt.Printf("Config not found. Run: amdl config init (target: %s)\n", cfgPath)
			return
		}
		if err := editConfig(cfgPath); err != nil {
			fmt.Printf("Failed to edit config: %v\n", err)
		}
	default:
		fmt.Println("Usage: amdl config <init|show|edit>")
	}
}

func handleLogin(args []string) {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		printLoginHelp()
		return
	}
	_ = args
	cfgPath, ok := resolveConfigPath()
	if !ok {
		if err := initConfigAt(cfgPath); err != nil {
			fmt.Printf("Config init failed: %v\n", err)
			return
		}
	}
	if err := editConfig(cfgPath); err != nil {
		fmt.Printf("Failed to open config editor: %v\n", err)
		return
	}
}

func handleSearchCmd(args []string) {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		printSearchHelp()
		return
	}
	if len(args) < 2 {
		fmt.Println("Usage: amdl search <song|album|artist|playlist> <query>")
		return
	}
	cfgPath, ok := resolveConfigPath()
	if !ok {
		fmt.Printf("Config not found. Run: amdl config init (target: %s)\n", cfgPath)
		return
	}
	if err := loadConfigFrom(cfgPath); err != nil {
		fmt.Printf("Failed to load config: %v\n", err)
		return
	}
	token, err := resolveToken()
	if err != nil {
		fmt.Printf("Failed to get token: %v\n", err)
		return
	}

	searchType := strings.ToLower(args[0])
	query := strings.Join(args[1:], " ")
	if !isValidSearchType(searchType) {
		fmt.Println("Invalid search type. Use: song, album, artist, playlist")
		return
	}

	results, err := searchAppleMusic(searchType, query, token)
	if err != nil {
		fmt.Printf("Search failed: %v\n", err)
		return
	}
	if len(results) == 0 {
		fmt.Println("No results.")
		return
	}
	for i, item := range results {
		fmt.Printf("%d. %s\n", i+1, formatSearchResult(item))
	}
}

func handleMetadata(args []string) {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		printMetadataHelp()
		return
	}
	fs := pflag.NewFlagSet("metadata", pflag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "Output JSON")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return
	}
	rest := fs.Args()
	if len(rest) < 1 {
		fmt.Println("Usage: amdl metadata <url> [--json]")
		return
	}
	cfgPath, ok := resolveConfigPath()
	if !ok {
		fmt.Printf("Config not found. Run: amdl config init (target: %s)\n", cfgPath)
		return
	}
	if err := loadConfigFrom(cfgPath); err != nil {
		fmt.Printf("Failed to load config: %v\n", err)
		return
	}
	token, err := resolveToken()
	if err != nil {
		fmt.Printf("Failed to get token: %v\n", err)
		return
	}

	meta, err := collectMetadata(rest[0], token)
	if err != nil {
		fmt.Printf("Metadata failed: %v\n", err)
		return
	}
	if *jsonOut {
		data, _ := json.MarshalIndent(meta, "", "  ")
		fmt.Println(string(data))
		return
	}
	fmt.Printf("Title: %s\n", meta.Title)
	fmt.Printf("Artist: %s\n", meta.Artist)
	fmt.Printf("Album: %s\n", meta.Album)
	fmt.Printf("Codec: %s\n", meta.Codec)
	if len(meta.Qualities) == 0 {
		fmt.Printf("Available Qualities: %s\n", "Unknown")
	} else {
		fmt.Printf("Available Qualities: %s\n", strings.Join(meta.Qualities, ", "))
	}
	fmt.Printf("Lyrics: %s\n", meta.Lyrics)
}

func handleDebug(args []string) {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		printDebugHelp()
		return
	}
	if len(args) < 1 {
		fmt.Println("Usage: amdl debug <url>")
		return
	}
	cfgPath, ok := resolveConfigPath()
	if !ok {
		fmt.Printf("Config not found. Run: amdl config init (target: %s)\n", cfgPath)
		return
	}
	if err := loadConfigFrom(cfgPath); err != nil {
		fmt.Printf("Failed to load config: %v\n", err)
		return
	}
	token, err := resolveToken()
	if err != nil {
		fmt.Printf("Failed to get token: %v\n", err)
		return
	}

	debug_mode = true
	logger.Init(true)

	if err := debugQualities(args[0], token); err != nil {
		fmt.Printf("Debug failed: %v\n", err)
	}
}

func handleDownload(args []string) {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		printDownloadHelp()
		return
	}
	resetRuntimeFlags()
	cfgPath, ok := resolveConfigPath()
	if !ok {
		fmt.Printf("Config not found. Run: amdl config init (target: %s)\n", cfgPath)
		return
	}
	if err := loadConfigFrom(cfgPath); err != nil {
		fmt.Printf("Failed to load config: %v\n", err)
		return
	}
	token, err := resolveToken()
	if err != nil {
		fmt.Printf("Failed to get token: %v\n", err)
		return
	}

	fs := pflag.NewFlagSet("download", pflag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var codec string
	var maxQuality string
	var lyrics bool
	var lyricsFormat string
	var lyricsType string
	var embedLyrics bool
	var saveLyrics bool
	var noCover bool
	var coverFileFlag bool
	var coverNameFlag string
	var coverSize string
	var coverFormat string
	var output string
	var threads int
	var selectFlag bool
	var preset string
	var convert string
	var keepOriginal bool

	fs.StringVar(&codec, "codec", "", "aac | alac | atmos")
	fs.StringVar(&maxQuality, "max-quality", "", "e.g. 192k or 2768")
	fs.BoolVar(&lyrics, "lyrics", false, "Enable lyrics download")
	fs.StringVar(&lyricsFormat, "lyrics-format", "", "ttml | lrc")
	fs.StringVar(&lyricsType, "lyrics-type", "", "plain | syllable")
	fs.BoolVar(&embedLyrics, "embed-lyrics", false, "Embed lyrics into audio")
	fs.BoolVar(&saveLyrics, "save-lyrics", false, "Save lyrics to file")
	fs.BoolVar(&noCover, "no-cover", false, "Disable cover embed/save")
	fs.BoolVar(&coverFileFlag, "cover-file", false, "Write cover image file")
	fs.StringVar(&coverNameFlag, "cover-name", "", "Cover file name (without extension)")
	fs.StringVar(&coverSize, "cover-size", "", "Cover size (e.g. 5000)")
	fs.StringVar(&coverFormat, "cover-format", "", "jpg | png | original")
	fs.StringVar(&output, "output", "", "Output directory")
	fs.IntVar(&threads, "threads", 0, "Number of parallel download threads")
	fs.BoolVar(&selectFlag, "select", false, "Interactively select tracks")
	fs.StringVar(&preset, "preset", "", "default | lossless | archival | minimal")
	fs.StringVar(&convert, "convert", "", "flac | mp3 | opus | wav")
	fs.BoolVar(&keepOriginal, "keep-original", false, "Keep original files after conversion")

	if err := fs.Parse(args); err != nil {
		return
	}

	rest := fs.Args()
	if len(rest) == 0 {
		fmt.Println("Usage: amdl download <url> [flags]")
		return
	}

	dlType := ""
	if len(rest) >= 2 && isDownloadType(rest[0]) {
		dlType = rest[0]
		rest = rest[1:]
	}

	applyPreset(preset)
	applyDownloadFlags(fs, downloadFlagOptions{
		codec:        codec,
		maxQuality:   maxQuality,
		lyrics:       lyrics,
		lyricsFormat: lyricsFormat,
		lyricsType:   lyricsType,
		embedLyrics:  embedLyrics,
		saveLyrics:   saveLyrics,
		noCover:      noCover,
		coverFile:    coverFileFlag,
		coverName:    coverNameFlag,
		coverSize:    coverSize,
		coverFormat:  coverFormat,
		output:       output,
		threads:      threads,
		selectFlag:   selectFlag,
		convert:      convert,
		keepOriginal: keepOriginal,
	})

	logger.Init(false)
	decryptSem = make(chan struct{}, 1)

	urls, err := resolveDownloadTargets(dlType, rest, token)
	if err != nil {
		fmt.Printf("Download target error: %v\n", err)
		return
	}
	if len(urls) == 0 {
		fmt.Println("No valid URLs provided.")
		return
	}

	if dlType == "artist" && len(urls) == 1 && strings.Contains(urls[0], "/artist/") {
		urlArtistName, urlArtistID, err := getUrlArtistName(urls[0], token)
		if err != nil {
			fmt.Println("Failed to get artist name.")
			return
		}
		Config.Naming.Artist = strings.NewReplacer(
			"{UrlArtistName}", LimitString(urlArtistName),
			"{ArtistId}", urlArtistID,
		).Replace(Config.Naming.Artist)
		albumArgs, err := checkArtist(urls[0], token, "albums")
		if err != nil {
			fmt.Println("Failed to get artist albums.")
			return
		}
		mvArgs, err := checkArtist(urls[0], token, "music-videos")
		if err != nil {
			fmt.Println("Failed to get artist music-videos.")
		}
		urls = append(albumArgs, mvArgs...)
	}

	logger.LogHook = tui.SendLog
	tui.Init()
	go func() {
		runDownloads(urls, token)
	}()
	if err := tui.Start(); err != nil {
		panic(err)
	}
}

type downloadFlagOptions struct {
	codec        string
	maxQuality   string
	lyrics       bool
	lyricsFormat string
	lyricsType   string
	embedLyrics  bool
	saveLyrics   bool
	noCover      bool
	coverFile    bool
	coverName    string
	coverSize    string
	coverFormat  string
	output       string
	threads      int
	selectFlag   bool
	convert      string
	keepOriginal bool
}

func applyDownloadFlags(fs *pflag.FlagSet, opts downloadFlagOptions) {
	if fs.Lookup("codec").Changed {
		switch strings.ToLower(opts.codec) {
		case "aac":
			dl_aac = true
			dl_atmos = false
			Config.Download.Codec.AacType = "aac"
		case "alac":
			dl_aac = false
			dl_atmos = false
		case "atmos":
			dl_atmos = true
			dl_aac = false
		default:
			fmt.Println("Invalid codec. Use aac, alac, or atmos.")
		}
	}
	if fs.Lookup("max-quality").Changed && opts.maxQuality != "" {
		if val, err := parseQualityValue(opts.maxQuality); err == nil {
			if dl_atmos {
				Config.Download.Codec.AtmosMax = val
			} else if dl_aac {
				// no-op for AAC
			} else {
				Config.Download.Codec.AlacMax = val
			}
		} else {
			fmt.Printf("Invalid max-quality: %v\n", err)
		}
	}
	if fs.Lookup("lyrics").Changed && opts.lyrics {
		Config.Lyrics.Enable = true
		if !Config.Lyrics.Embed && !Config.Lyrics.SaveFile {
			Config.Lyrics.Embed = true
		}
	}
	if fs.Lookup("lyrics-format").Changed && opts.lyricsFormat != "" {
		Config.Lyrics.Enable = true
		switch strings.ToLower(opts.lyricsFormat) {
		case "ttml", "lrc":
			Config.Lyrics.Format = strings.ToLower(opts.lyricsFormat)
			if !Config.Lyrics.Embed && !Config.Lyrics.SaveFile {
				Config.Lyrics.Embed = true
			}
		default:
			fmt.Println("Invalid lyrics-format. Use ttml or lrc.")
		}
	}
	if fs.Lookup("lyrics-type").Changed && opts.lyricsType != "" {
		Config.Lyrics.Enable = true
		switch strings.ToLower(opts.lyricsType) {
		case "plain":
			Config.Lyrics.Type = "lyrics"
		case "syllable":
			Config.Lyrics.Type = "syllable-lyrics"
		default:
			fmt.Println("Invalid lyrics-type. Use plain or syllable.")
		}
		if !Config.Lyrics.Embed && !Config.Lyrics.SaveFile {
			Config.Lyrics.Embed = true
		}
	}
	if fs.Lookup("embed-lyrics").Changed && opts.embedLyrics {
		Config.Lyrics.Enable = true
		Config.Lyrics.Embed = true
	}
	if fs.Lookup("save-lyrics").Changed && opts.saveLyrics {
		Config.Lyrics.Enable = true
		Config.Lyrics.SaveFile = true
	}
	if fs.Lookup("no-cover").Changed && opts.noCover {
		coverDisabled = true
		Config.Artwork.EmbedCover = false
	}
	if fs.Lookup("cover-file").Changed && opts.coverFile {
		coverFile = true
		if !coverDisabled {
			Config.Artwork.EmbedCover = true
		}
	}
	if fs.Lookup("cover-name").Changed {
		coverName = opts.coverName
	}
	if fs.Lookup("cover-size").Changed && opts.coverSize != "" {
		Config.Artwork.Size = normalizeCoverSize(opts.coverSize)
	}
	if fs.Lookup("cover-format").Changed && opts.coverFormat != "" {
		switch strings.ToLower(opts.coverFormat) {
		case "jpg", "png", "original":
			Config.Artwork.Format = strings.ToLower(opts.coverFormat)
		default:
			fmt.Println("Invalid cover-format. Use jpg, png, or original.")
		}
	}
	if fs.Lookup("output").Changed && opts.output != "" {
		out := expandPath(opts.output)
		Config.Paths.Alac = out
		Config.Paths.Atmos = out
		Config.Paths.Aac = out
	}
	if fs.Lookup("threads").Changed && opts.threads > 0 {
		Config.Download.Threads = opts.threads
	}
	if fs.Lookup("select").Changed && opts.selectFlag {
		dl_select = true
	}
	if fs.Lookup("convert").Changed && opts.convert != "" {
		switch strings.ToLower(opts.convert) {
		case "flac", "mp3", "opus", "wav":
			Config.Convert.Enable = true
			Config.Convert.Format = strings.ToLower(opts.convert)
		default:
			fmt.Println("Invalid convert format. Use flac, mp3, opus, or wav.")
		}
	}
	if fs.Lookup("keep-original").Changed && opts.keepOriginal {
		Config.Convert.KeepOriginal = true
	}
}

func applyPreset(preset string) {
	switch strings.ToLower(preset) {
	case "", "default":
		return
	case "lossless":
		dl_aac = false
		dl_atmos = false
		Config.Lyrics.Enable = true
		Config.Artwork.EmbedCover = true
	case "archival":
		dl_aac = false
		dl_atmos = false
		Config.Lyrics.Enable = true
		Config.Lyrics.Embed = true
		Config.Lyrics.SaveFile = true
		Config.Artwork.Size = normalizeCoverSize("5000")
	case "minimal":
		dl_aac = true
		dl_atmos = false
		Config.Download.Codec.AacType = "aac"
		Config.Lyrics.Enable = false
		Config.Lyrics.Embed = false
		Config.Lyrics.SaveFile = false
		coverDisabled = true
		Config.Artwork.EmbedCover = false
	default:
		fmt.Println("Invalid preset. Use default, lossless, archival, or minimal.")
	}
}

func resolveToken() (string, error) {
	token, err := ampapi.GetToken()
	if err == nil && token != "" {
		return token, nil
	}
	if Config.Auth.AuthorizationToken != "" && Config.Auth.AuthorizationToken != "your-authorization-token" {
		return strings.Replace(Config.Auth.AuthorizationToken, "Bearer ", "", -1), nil
	}
	if err != nil {
		return "", err
	}
	return "", errors.New("token unavailable")
}

func isDownloadType(s string) bool {
	switch strings.ToLower(s) {
	case "song", "album", "playlist", "artist":
		return true
	default:
		return false
	}
}

func isValidSearchType(s string) bool {
	switch strings.ToLower(s) {
	case "song", "album", "artist", "playlist":
		return true
	default:
		return false
	}
}

func normalizeCoverSize(val string) string {
	if strings.Contains(val, "x") {
		return val
	}
	return fmt.Sprintf("%sx%s", val, val)
}

func expandPath(p string) string {
	if p == "~" {
		home, err := os.UserHomeDir()
		if err == nil {
			return home
		}
	}
	if strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~/"))
		}
	}
	return p
}

func parseQualityValue(val string) (int, error) {
	re := regexp.MustCompile(`(?i)^(\d+)(k?)$`)
	match := re.FindStringSubmatch(strings.TrimSpace(val))
	if len(match) != 3 {
		return 0, fmt.Errorf("invalid quality value: %s", val)
	}
	num, _ := strconv.Atoi(match[1])
	if strings.ToLower(match[2]) == "k" {
		return num * 1000, nil
	}
	return num, nil
}

type MetadataOutput struct {
	Title     string   `json:"title"`
	Artist    string   `json:"artist"`
	Album     string   `json:"album"`
	Codec     string   `json:"codec"`
	Qualities []string `json:"qualities"`
	Lyrics    string   `json:"lyrics"`
}

func collectMetadata(input string, token string) (*MetadataOutput, error) {
	ref, err := resolveInputReference(input, token)
	if err != nil {
		return nil, err
	}

	switch ref.Kind {
	case "song":
		resp, err := ampapi.GetSongResp(ref.Storefront, ref.ID, Config.Auth.Language, token)
		if err != nil {
			return nil, err
		}
		attrs := resp.Data[0].Attributes
		qualities := []string{}
		if attrs.ExtendedAssetUrls.EnhancedHls != "" {
			qualities, _ = listQualities(attrs.ExtendedAssetUrls.EnhancedHls)
		}
		return &MetadataOutput{
			Title:     attrs.Name,
			Artist:    attrs.ArtistName,
			Album:     attrs.AlbumName,
			Codec:     inferCodec(qualities, attrs.AudioTraits),
			Qualities: qualities,
			Lyrics:    formatLyricsAvailability(attrs.HasLyrics, attrs.HasTimeSyncedLyrics),
		}, nil
	case "album":
		resp, err := ampapi.GetAlbumResp(ref.Storefront, ref.ID, Config.Auth.Language, token)
		if err != nil {
			return nil, err
		}
		attrs := resp.Data[0].Attributes
		qualities, _ := qualitiesFromFirstTrack(ref.Storefront, resp.Data[0].Relationships.Tracks.Data, token)
		return &MetadataOutput{
			Title:     attrs.Name,
			Artist:    attrs.ArtistName,
			Album:     attrs.Name,
			Codec:     inferCodec(qualities, attrs.AudioTraits),
			Qualities: qualities,
			Lyrics:    "varies",
		}, nil
	case "playlist":
		resp, err := ampapi.GetPlaylistResp(ref.Storefront, ref.ID, Config.Auth.Language, token)
		if err != nil {
			return nil, err
		}
		attrs := resp.Data[0].Attributes
		qualities, _ := qualitiesFromFirstTrack(ref.Storefront, resp.Data[0].Relationships.Tracks.Data, token)
		return &MetadataOutput{
			Title:     attrs.Name,
			Artist:    attrs.ArtistName,
			Album:     attrs.Name,
			Codec:     inferCodec(qualities, attrs.AudioTraits),
			Qualities: qualities,
			Lyrics:    "varies",
		}, nil
	case "artist":
		name, _, err := getUrlArtistName(ref.URL, token)
		if err != nil {
			return nil, err
		}
		return &MetadataOutput{
			Title:     name,
			Artist:    name,
			Album:     "",
			Codec:     "",
			Qualities: nil,
			Lyrics:    "",
		}, nil
	default:
		return nil, fmt.Errorf("unsupported metadata type: %s", ref.Kind)
	}
}

func qualitiesFromFirstTrack(storefront string, tracks []ampapi.TrackRespData, token string) ([]string, error) {
	if len(tracks) == 0 {
		return nil, nil
	}
	manifest, err := ampapi.GetSongResp(storefront, tracks[0].ID, Config.Auth.Language, token)
	if err != nil {
		return nil, err
	}
	if manifest.Data[0].Attributes.ExtendedAssetUrls.EnhancedHls == "" {
		return nil, nil
	}
	return listQualities(manifest.Data[0].Attributes.ExtendedAssetUrls.EnhancedHls)
}

func formatLyricsAvailability(hasLyrics bool, hasSynced bool) string {
	if hasSynced {
		return "synced"
	}
	if hasLyrics {
		return "available"
	}
	return "none"
}

func inferCodec(qualities []string, traits []string) string {
	if len(qualities) > 0 {
		return strings.Join(qualities, " | ")
	}
	if len(traits) > 0 {
		return strings.Join(traits, ", ")
	}
	return ""
}

func listQualities(masterURL string) ([]string, error) {
	resp, err := http.Get(masterURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("m3u8 request failed: %s", resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	from, listType, err := m3u8.DecodeFrom(strings.NewReader(string(body)), true)
	if err != nil || listType != m3u8.MASTER {
		return nil, errors.New("m3u8 not of master type")
	}
	master := from.(*m3u8.MasterPlaylist)

	var hasAAC, hasLossless, hasHiRes, hasAtmos, hasDolbyAudio bool
	var aacQuality, losslessQuality, hiResQuality, atmosQuality, dolbyAudioQuality string

	for _, variant := range master.Variants {
		if variant.Codecs == "mp4a.40.2" {
			hasAAC = true
			split := strings.Split(variant.Audio, "-")
			if len(split) >= 3 {
				bitrate, _ := strconv.Atoi(split[2])
				currentBitrate := 0
				if aacQuality != "" {
					current := strings.Split(aacQuality, " | ")[2]
					current = strings.Split(current, " ")[0]
					currentBitrate, _ = strconv.Atoi(current)
				}
				if bitrate > currentBitrate {
					aacQuality = fmt.Sprintf("AAC | 2 Channel | %d Kbps", bitrate)
				}
			}
		} else if variant.Codecs == "ec-3" && strings.Contains(variant.Audio, "atmos") {
			hasAtmos = true
			split := strings.Split(variant.Audio, "-")
			if len(split) > 0 {
				bitrateStr := split[len(split)-1]
				if len(bitrateStr) == 4 && bitrateStr[0] == '2' {
					bitrateStr = bitrateStr[1:]
				}
				bitrate, _ := strconv.Atoi(bitrateStr)
				currentBitrate := 0
				if atmosQuality != "" {
					current := strings.Split(strings.Split(atmosQuality, " | ")[2], " ")[0]
					currentBitrate, _ = strconv.Atoi(current)
				}
				if bitrate > currentBitrate {
					atmosQuality = fmt.Sprintf("E-AC-3 | 16 Channel | %d Kbps", bitrate)
				}
			}
		} else if variant.Codecs == "alac" {
			split := strings.Split(variant.Audio, "-")
			if len(split) >= 3 {
				bitDepth := split[len(split)-1]
				sampleRate := split[len(split)-2]
				sampleRateInt, _ := strconv.Atoi(sampleRate)
				if sampleRateInt > 48000 {
					hasHiRes = true
					hiResQuality = fmt.Sprintf("ALAC | 2 Channel | %s-bit/%d kHz", bitDepth, sampleRateInt/1000)
				} else {
					hasLossless = true
					losslessQuality = fmt.Sprintf("ALAC | 2 Channel | %s-bit/%d kHz", bitDepth, sampleRateInt/1000)
				}
			}
		} else if variant.Codecs == "ac-3" {
			hasDolbyAudio = true
			split := strings.Split(variant.Audio, "-")
			if len(split) > 0 {
				bitrate, _ := strconv.Atoi(split[len(split)-1])
				dolbyAudioQuality = fmt.Sprintf("AC-3 | 16 Channel | %d Kbps", bitrate)
			}
		}
	}

	var out []string
	if hasAAC {
		out = append(out, aacQuality)
	}
	if hasLossless {
		out = append(out, losslessQuality)
	}
	if hasHiRes {
		out = append(out, hiResQuality)
	}
	if hasAtmos {
		out = append(out, atmosQuality)
	}
	if hasDolbyAudio {
		out = append(out, dolbyAudioQuality)
	}
	return out, nil
}

type inputReference struct {
	Kind       string
	Storefront string
	ID         string
	URL        string
}

func resolveInputReference(input string, token string) (*inputReference, error) {
	if strings.HasPrefix(input, "am:") {
		return resolveAMReference(input, token)
	}
	if storefront, id := checkUrlSong(input); storefront != "" && id != "" {
		return &inputReference{Kind: "song", Storefront: storefront, ID: id, URL: input}, nil
	}
	if storefront, id := checkUrl(input); storefront != "" && id != "" {
		return &inputReference{Kind: "album", Storefront: storefront, ID: id, URL: input}, nil
	}
	if storefront, id := checkUrlPlaylist(input); storefront != "" && id != "" {
		return &inputReference{Kind: "playlist", Storefront: storefront, ID: id, URL: input}, nil
	}
	if storefront, id := checkUrlArtist(input); storefront != "" && id != "" {
		return &inputReference{Kind: "artist", Storefront: storefront, ID: id, URL: input}, nil
	}
	if storefront, id := checkUrlMv(input); storefront != "" && id != "" {
		return &inputReference{Kind: "music-video", Storefront: storefront, ID: id, URL: input}, nil
	}
	if storefront, id := checkUrlStation(input); storefront != "" && id != "" {
		return &inputReference{Kind: "station", Storefront: storefront, ID: id, URL: input}, nil
	}
	return nil, fmt.Errorf("unrecognized input: %s", input)
}

func resolveAMReference(input string, token string) (*inputReference, error) {
	raw := strings.TrimPrefix(input, "am:")
	parts := strings.Split(raw, ":")
	var kind string
	var id string
	if len(parts) == 1 {
		id = parts[0]
	} else {
		kind = parts[0]
		id = parts[1]
	}
	if kind != "" {
		return resolveAMReferenceByKind(kind, id, token)
	}
	if ref, err := resolveAMReferenceByKind("song", id, token); err == nil {
		return ref, nil
	}
	if ref, err := resolveAMReferenceByKind("album", id, token); err == nil {
		return ref, nil
	}
	if ref, err := resolveAMReferenceByKind("playlist", id, token); err == nil {
		return ref, nil
	}
	if ref, err := resolveAMReferenceByKind("artist", id, token); err == nil {
		return ref, nil
	}
	return nil, fmt.Errorf("unable to resolve am id: %s", input)
}

func resolveAMReferenceByKind(kind string, id string, token string) (*inputReference, error) {
	storefront := Config.Auth.Storefront
	switch kind {
	case "song":
		resp, err := ampapi.GetSongResp(storefront, id, Config.Auth.Language, token)
		if err != nil || len(resp.Data) == 0 {
			return nil, errors.New("not a song")
		}
		return &inputReference{Kind: "song", Storefront: storefront, ID: id, URL: resp.Data[0].Attributes.URL}, nil
	case "album":
		resp, err := ampapi.GetAlbumResp(storefront, id, Config.Auth.Language, token)
		if err != nil || len(resp.Data) == 0 {
			return nil, errors.New("not an album")
		}
		return &inputReference{Kind: "album", Storefront: storefront, ID: id, URL: resp.Data[0].Attributes.URL}, nil
	case "playlist":
		resp, err := ampapi.GetPlaylistResp(storefront, id, Config.Auth.Language, token)
		if err != nil || len(resp.Data) == 0 {
			return nil, errors.New("not a playlist")
		}
		return &inputReference{Kind: "playlist", Storefront: storefront, ID: id, URL: resp.Data[0].Attributes.URL}, nil
	case "artist":
		url, err := getArtistURLByID(storefront, id, token)
		if err != nil {
			return nil, err
		}
		return &inputReference{Kind: "artist", Storefront: storefront, ID: id, URL: url}, nil
	default:
		return nil, fmt.Errorf("unknown kind: %s", kind)
	}
}

func resolveDownloadTargets(dlType string, inputs []string, token string) ([]string, error) {
	var out []string
	for _, input := range inputs {
		ref, err := resolveInputReferenceWithTypeHint(input, dlType, token)
		if err != nil {
			return nil, err
		}
		out = append(out, ref.URL)
	}
	return out, nil
}

func resolveInputReferenceWithTypeHint(input string, hint string, token string) (*inputReference, error) {
	if strings.HasPrefix(input, "am:") {
		raw := strings.TrimPrefix(input, "am:")
		if hint != "" && !strings.Contains(raw, ":") {
			return resolveAMReferenceByKind(hint, raw, token)
		}
		return resolveAMReference(input, token)
	}
	if hint != "" {
		switch hint {
		case "song":
			if storefront, id := checkUrlSong(input); storefront != "" && id != "" {
				return &inputReference{Kind: "song", Storefront: storefront, ID: id, URL: input}, nil
			}
			if songURL, ok, err := resolveSongURLFromAlbumURL(input, token); ok && err == nil {
				return &inputReference{Kind: "song", Storefront: Config.Auth.Storefront, ID: "", URL: songURL}, nil
			} else if err != nil {
				return nil, err
			}
		case "album":
			if storefront, id := checkUrl(input); storefront != "" && id != "" {
				return &inputReference{Kind: "album", Storefront: storefront, ID: id, URL: input}, nil
			}
		case "playlist":
			if storefront, id := checkUrlPlaylist(input); storefront != "" && id != "" {
				return &inputReference{Kind: "playlist", Storefront: storefront, ID: id, URL: input}, nil
			}
		case "artist":
			if storefront, id := checkUrlArtist(input); storefront != "" && id != "" {
				return &inputReference{Kind: "artist", Storefront: storefront, ID: id, URL: input}, nil
			}
		}
	}
	return resolveInputReference(input, token)
}

func resolveSongURLFromAlbumURL(input string, token string) (string, bool, error) {
	parsed, err := url.Parse(input)
	if err != nil {
		return "", false, err
	}
	songID := parsed.Query().Get("i")
	if songID == "" {
		return "", false, nil
	}
	storefront := Config.Auth.Storefront
	if sf, _ := checkUrl(input); sf != "" {
		storefront = sf
	}
	resp, err := ampapi.GetSongResp(storefront, songID, Config.Auth.Language, token)
	if err != nil || len(resp.Data) == 0 {
		return "", false, err
	}
	return resp.Data[0].Attributes.URL, true, nil
}

func getArtistURLByID(storefront string, id string, token string) (string, error) {
	req, err := http.NewRequest("GET", fmt.Sprintf("https://amp-api.music.apple.com/v1/catalog/%s/artists/%s", storefront, id), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Origin", "https://music.apple.com")
	query := req.URL.Query()
	query.Set("l", Config.Auth.Language)
	req.URL.RawQuery = query.Encode()
	do, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer do.Body.Close()
	if do.StatusCode != http.StatusOK {
		return "", fmt.Errorf("artist lookup failed: %s", do.Status)
	}
	var payload struct {
		Data []struct {
			Attributes struct {
				URL string `json:"url"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if err := json.NewDecoder(do.Body).Decode(&payload); err != nil {
		return "", err
	}
	if len(payload.Data) == 0 {
		return "", errors.New("artist not found")
	}
	return payload.Data[0].Attributes.URL, nil
}

func searchAppleMusic(searchType string, query string, token string) ([]SearchResultItem, error) {
	limit := 10
	offset := 0
	apiSearchType := searchType + "s"
	if searchType == "playlist" {
		apiSearchType = "playlists"
	}
	resp, err := ampapi.Search(Config.Auth.Storefront, query, apiSearchType, Config.Auth.Language, token, limit, offset)
	if err != nil {
		return nil, err
	}

	var results []SearchResultItem
	switch searchType {
	case "album":
		if resp.Results.Albums != nil {
			for _, item := range resp.Results.Albums.Data {
				results = append(results, SearchResultItem{
					Type:   "Album",
					Name:   item.Attributes.Name,
					Detail: item.Attributes.ArtistName,
					URL:    item.Attributes.URL,
					ID:     item.ID,
				})
			}
		}
	case "song":
		if resp.Results.Songs != nil {
			for _, item := range resp.Results.Songs.Data {
				results = append(results, SearchResultItem{
					Type:   "Song",
					Name:   item.Attributes.Name,
					Detail: item.Attributes.ArtistName,
					URL:    item.Attributes.URL,
					ID:     item.ID,
				})
			}
		}
	case "artist":
		if resp.Results.Artists != nil {
			for _, item := range resp.Results.Artists.Data {
				results = append(results, SearchResultItem{
					Type:   "Artist",
					Name:   item.Attributes.Name,
					Detail: "",
					URL:    item.Attributes.URL,
					ID:     item.ID,
				})
			}
		}
	case "playlist":
		if resp.Results.Playlists != nil {
			for _, item := range resp.Results.Playlists.Data {
				results = append(results, SearchResultItem{
					Type:   "Playlist",
					Name:   item.Attributes.Name,
					Detail: item.Attributes.CuratorName,
					URL:    item.Attributes.URL,
					ID:     item.ID,
				})
			}
		}
	}
	return results, nil
}

func formatSearchResult(item SearchResultItem) string {
	switch item.Type {
	case "Artist":
		return fmt.Sprintf("%s (am:%s)", item.Name, item.ID)
	case "Playlist":
		if item.Detail != "" {
			return fmt.Sprintf("%s - %s (am:%s)", item.Detail, item.Name, item.ID)
		}
		return fmt.Sprintf("%s (am:%s)", item.Name, item.ID)
	default:
		if item.Detail != "" {
			return fmt.Sprintf("%s - %s (am:%s)", item.Detail, item.Name, item.ID)
		}
		return fmt.Sprintf("%s (am:%s)", item.Name, item.ID)
	}
}

func debugQualities(input string, token string) error {
	ref, err := resolveInputReference(input, token)
	if err != nil {
		return err
	}
	switch ref.Kind {
	case "song":
		resp, err := ampapi.GetSongResp(ref.Storefront, ref.ID, Config.Auth.Language, token)
		if err != nil {
			return err
		}
		hls := resp.Data[0].Attributes.ExtendedAssetUrls.EnhancedHls
		if hls == "" {
			return errors.New("no enhanced hls available")
		}
		_, _, err = extractMedia(hls, true)
		return err
	case "album":
		resp, err := ampapi.GetAlbumResp(ref.Storefront, ref.ID, Config.Auth.Language, token)
		if err != nil {
			return err
		}
		return debugFirstTrack(ref.Storefront, resp.Data[0].Relationships.Tracks.Data, token)
	case "playlist":
		resp, err := ampapi.GetPlaylistResp(ref.Storefront, ref.ID, Config.Auth.Language, token)
		if err != nil {
			return err
		}
		return debugFirstTrack(ref.Storefront, resp.Data[0].Relationships.Tracks.Data, token)
	default:
		return fmt.Errorf("unsupported debug type: %s", ref.Kind)
	}
}

func debugFirstTrack(storefront string, tracks []ampapi.TrackRespData, token string) error {
	if len(tracks) == 0 {
		return errors.New("no tracks found")
	}
	manifest, err := ampapi.GetSongResp(storefront, tracks[0].ID, Config.Auth.Language, token)
	if err != nil {
		return err
	}
	hls := manifest.Data[0].Attributes.ExtendedAssetUrls.EnhancedHls
	if hls == "" {
		return errors.New("no enhanced hls available")
	}
	_, _, err = extractMedia(hls, true)
	return err
}

func resetRuntimeFlags() {
	dl_atmos = false
	dl_aac = false
	dl_select = false
	dl_song = false
	debug_mode = false
	coverFile = false
	coverName = ""
	coverDisabled = false
}

func printDownloadHelp() {
	fmt.Println("Usage:")
	fmt.Println("  amdl download <url> [flags]")
	fmt.Println("  amdl download <song|album|playlist|artist> <url> [flags]")
	fmt.Println()
	fmt.Println("Flags:")
	fmt.Println("  --codec <aac|alac|atmos>        audio codec")
	fmt.Println("  --max-quality <value>          max quality (e.g. 192k, 2768)")
	fmt.Println("  --lyrics                       enable lyrics download")
	fmt.Println("  --lyrics-format <ttml|lrc>     lyrics format")
	fmt.Println("  --lyrics-type <plain|syllable> lyrics type")
	fmt.Println("  --embed-lyrics                 embed lyrics")
	fmt.Println("  --save-lyrics                  save lyrics file")
	fmt.Println("  --no-cover                     disable cover embed/save")
	fmt.Println("  --cover-file                   write cover image file")
	fmt.Println("  --cover-name <name>            cover file base name")
	fmt.Println("  --cover-size <size>            cover size (e.g. 5000)")
	fmt.Println("  --cover-format <jpg|png|original> cover format")
	fmt.Println("  --output <path>                output directory")
	fmt.Println("  --threads <num>                parallel download threads")
	fmt.Println("  --select                       interactive track selection")
	fmt.Println("  --preset <default|lossless|archival|minimal> preset mode")
	fmt.Println("  --convert <flac|mp3|opus|wav>  convert after download")
	fmt.Println("  --keep-original                keep original files after conversion")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  amdl download https://music.apple.com/.../album/...")
	fmt.Println("  amdl download song https://music.apple.com/.../song/...")
	fmt.Println("  amdl download <url> --codec alac --lyrics --embed-lyrics")
	fmt.Println("  amdl download <url> --preset archival")
}

func printSearchHelp() {
	fmt.Println("Usage:")
	fmt.Println("  amdl search <song|album|artist|playlist> <query>")
	fmt.Println()
	fmt.Println("Example:")
	fmt.Println("  amdl search song \"yoasobi\"")
	fmt.Println("  amdl search artist \"Utada Hikaru\"")
}

func printMetadataHelp() {
	fmt.Println("Usage:")
	fmt.Println("  amdl metadata <url> [--json]")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  amdl metadata https://music.apple.com/.../album/...")
	fmt.Println("  amdl metadata https://music.apple.com/.../song/... --json")
}

func printConfigHelp() {
	fmt.Println("Usage:")
	fmt.Println("  amdl config init")
	fmt.Println("  amdl config show")
	fmt.Println("  amdl config edit")
	fmt.Println()
	fmt.Println("Notes:")
	fmt.Println("  Default path: ~/.config/amdl/config.yaml")
	fmt.Println("  Override path via AMDL_CONFIG environment variable")
}

func printLoginHelp() {
	fmt.Println("Usage:")
	fmt.Println("  amdl login")
	fmt.Println()
	fmt.Println("Opens config file in $EDITOR for auth setup.")
}

func printDebugHelp() {
	fmt.Println("Usage:")
	fmt.Println("  amdl debug <url>")
	fmt.Println()
	fmt.Println("Shows available audio variants/qualities.")
}
