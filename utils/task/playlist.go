package task

import (
	"errors"
	"fmt"

	"main/utils/ampapi"
	"main/utils/tui"
)

type Playlist struct {
	Storefront string
	ID         string

	SaveDir   string
	SaveName  string
	Codec     string
	CoverPath string

	Language string
	Resp     ampapi.PlaylistResp
	Name     string
	Tracks   []Track
}

func NewPlaylist(st string, id string) *Playlist {
	a := new(Playlist)
	a.Storefront = st
	a.ID = id

	//fmt.Println("Album created")
	return a

}

func (a *Playlist) GetResp(token, l string) error {
	var err error
	a.Language = l
	resp, err := ampapi.GetPlaylistResp(a.Storefront, a.ID, a.Language, token)
	if err != nil {
		return errors.New("error getting album response")
	}
	a.Resp = *resp

	a.Resp.Data[0].Attributes.ArtistName = "Apple Music"
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

			Resp:    trackData,
			PreType: "playlists",
			//DiscTotal: a.Resp.Data[0].Relationships.Tracks.Data[len-1].Attributes.DiscNumber, 在它处获取
			PreID:        a.ID,
			PlaylistData: a.Resp.Data[0],
		})
	}
	return nil
}

func (a *Playlist) GetArtwork() string {
	return a.Resp.Data[0].Attributes.Artwork.URL
}

func (a *Playlist) ShowSelect() []int {
	meta := a.Resp
	trackTotal := len(meta.Data[0].Relationships.Tracks.Data)
	arr := make([]int, trackTotal)
	for i := 0; i < trackTotal; i++ {
		arr[i] = i + 1
	}

	var items []string
	for trackNum, track := range meta.Data[0].Relationships.Tracks.Data {
		trackNum++
		trackName := fmt.Sprintf("%s - %s", track.Attributes.Name, track.Attributes.ArtistName)

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

		item := fmt.Sprintf("%-40s | %-4s | %s", truncate(trackName, 40), rating, typeStr)
		items = append(items, item)
	}

	title := fmt.Sprintf("Playlists: %d tracks", trackTotal)

	selected := tui.RequestSelection(title, items)
	return selected
}
