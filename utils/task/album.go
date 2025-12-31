package task

import (
	"errors"
	"fmt"
	"strings"

	"main/utils/ampapi"
	"main/utils/tui"
)

type Album struct {
	Storefront string
	ID         string

	SaveDir   string
	SaveName  string
	Codec     string
	CoverPath string

	Language string
	Resp     ampapi.AlbumResp
	Name     string
	Tracks   []Track
}

func NewAlbum(st string, id string) *Album {
	a := new(Album)
	a.Storefront = st
	a.ID = id

	//fmt.Println("Album created")
	return a

}

func (a *Album) GetResp(token, l string) error {
	var err error
	a.Language = l
	resp, err := ampapi.GetAlbumResp(a.Storefront, a.ID, a.Language, token)
	if err != nil {
		return errors.New("error getting album response")
	}
	a.Resp = *resp
	//简化高频调用名称
	a.Name = a.Resp.Data[0].Attributes.Name
	//fmt.Println("Getting album response")
	//从resp中的Tracks数据中提取trackData信息到新的Track结构体中
	for i, trackData := range a.Resp.Data[0].Relationships.Tracks.Data {
		len := len(a.Resp.Data[0].Relationships.Tracks.Data)
		a.Tracks = append(a.Tracks, Track{
			ID:         trackData.ID,
			Type:       trackData.Type,
			Name:       trackData.Attributes.Name,
			Language:   a.Language,
			Storefront: a.Storefront,

			//SaveDir:   filepath.Join(a.SaveDir, a.SaveName),
			//Codec:     a.Codec,
			TaskNum:   i + 1,
			TaskTotal: len,
			M3u8:      trackData.Attributes.ExtendedAssetUrls.EnhancedHls,
			WebM3u8:   trackData.Attributes.ExtendedAssetUrls.EnhancedHls,
			//CoverPath: a.CoverPath,

			Resp:      trackData,
			PreType:   "albums",
			DiscTotal: a.Resp.Data[0].Relationships.Tracks.Data[len-1].Attributes.DiscNumber,
			PreID:     a.ID,
			AlbumData: a.Resp.Data[0],
		})
	}
	return nil
}

func (a *Album) GetArtwork() string {
	return a.Resp.Data[0].Attributes.Artwork.URL
}

func (a *Album) ShowSelect() []int {
	meta := a.Resp
	trackTotal := len(meta.Data[0].Relationships.Tracks.Data)
	arr := make([]int, trackTotal)
	for i := 0; i < trackTotal; i++ {
		arr[i] = i + 1
	}

	var items []string
	for trackNum, track := range meta.Data[0].Relationships.Tracks.Data {
		trackNum++
		trackName := fmt.Sprintf("%02d. %s", track.Attributes.TrackNumber, track.Attributes.Name)

		rating := track.Attributes.ContentRating
		if rating == "explicit" {
			rating = "E"
		} else if rating == "clean" {
			rating = "C"
		} else {
			rating = "None"
		}

		typeStr := track.Type
		if typeStr == "music-videos" {
			typeStr = "MV"
		} else if typeStr == "songs" {
			typeStr = "SONG"
		}

		// Align columns roughly
		// Name is variable, so let's just append
		item := fmt.Sprintf("%-40s | %-4s | %s", truncate(trackName, 40), rating, typeStr)
		items = append(items, item)
	}

	title := fmt.Sprintf("Storefront: %s, %d tracks missing", strings.ToUpper(a.Storefront), meta.Data[0].Attributes.TrackCount-trackTotal)

	selected := tui.RequestSelection(title, items)
	if len(selected) == 0 {
		// If user cancelled (esc), maybe return empty or all?
		// Original logic: if "all" or empty (default), selected all?
		// Wait, original logic: user types "all".
		// If I return empty list here, main.go logic says:
		// if !dl_select { selected = arr } else { selected = ShowSelect() }
		// So if ShowSelect returns empty, main.go sees empty selected list.
		// If user cancels, we probably want to select NONE, which returns empty list.
		// Main loop iterates over selected. If empty, nothing happens.
		// That seems correct for "Cancel".
		return selected
	}
	return selected
}

func truncate(s string, max int) string {
	if len(s) > max {
		return s[:max-3] + "..."
	}
	return s
}
