package runv2

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/grafov/m3u8"
	"github.com/itouakirai/mp4ff/mp4"

	"encoding/binary"

	"github.com/schollz/progressbar/v3"

	"main/utils/structs"
)

const prefetchKey = "skd://itunes.apple.com/P000000000/s1/e1"

var ErrTimeout = errors.New("response timed out")

// DecryptionContext holds the information needed to decrypt a downloaded file
type DecryptionContext struct {
	AdamId           string
	PlaylistSegments []*m3u8.MediaSegment
	TotalLen         int64
	Config           structs.ConfigSet
}

type ProgressWriter struct {
	Total      int64
	Current    int64
	Callback   structs.ProgressCallback
	Msg        string
	StartTime  time.Time
	LastUpdate time.Time
}

func (pw *ProgressWriter) Update(n int) {
	pw.Current += int64(n)

	if pw.StartTime.IsZero() {
		pw.StartTime = time.Now()
		pw.LastUpdate = time.Now()
	}

	if pw.Total > 0 && pw.Callback != nil {
		percent := float64(pw.Current) / float64(pw.Total)

		var bps float64
		// Calculate speed
		duration := time.Since(pw.StartTime).Seconds()
		var speed string
		if duration > 0 {
			bps = float64(pw.Current) / duration
			speed = formatSpeed(bps)
		} else {
			speed = "0 B/s"
		}

		// Limit updates to avoid overwhelming the TUI (e.g. every 100ms) or when finished
		if time.Since(pw.LastUpdate) > 100*time.Millisecond || percent >= 1.0 {
			pw.Callback(percent, fmt.Sprintf("%s (%s)", pw.Msg, speed), bps)
			pw.LastUpdate = time.Now()
		}
	}
}

func (pw *ProgressWriter) Write(p []byte) (int, error) {
	n := len(p)
	pw.Update(n)
	return n, nil
}

func formatSpeed(bps float64) string {
	if bps >= 1024*1024 {
		return fmt.Sprintf("%.2f MB/s", bps/(1024*1024))
	} else if bps >= 1024 {
		return fmt.Sprintf("%.2f KB/s", bps/1024)
	}
	return fmt.Sprintf("%.0f B/s", bps)
}

type TimedResponseBody struct {
	timeout   time.Duration
	timer     *time.Timer
	threshold int
	body      io.Reader
}

func (b *TimedResponseBody) Read(p []byte) (int, error) {
	n, err := b.body.Read(p)
	if err != nil {
		return n, err
	}
	// fmt.Printf("Read %d bytes, buffer size %d bytes", n, len(p))
	if n >= b.threshold {
		b.timer.Reset(b.timeout)
	}
	return n, err
}

func Run(adamId string, playlistUrl string, outfile string, Config structs.ConfigSet, cb structs.ProgressCallback) error {
	// Fallback to sequential mode if split is not desired, but here we implement the split logic.
	// Actually, for backward compatibility, we can just call Download then Decrypt.
	// But splitting them allows the caller to manage concurrency.
	// For now, let's keep Run as is for existing callers, but implement Download and Decrypt.
	// Wait, existing callers (main.go) will be updated.
	// So we can remove the old Run or rewrite it to use the new functions.

	ctx, err := Download(adamId, playlistUrl, outfile, Config, cb)
	if err != nil {
		return err
	}

	return Decrypt(ctx, outfile, outfile, cb) // Decrypt in place or to same file (Download saves to outfile directly currently)
}

// Download downloads the encrypted file content to outfile and returns the decryption context.
func Download(adamId string, playlistUrl string, outfile string, Config structs.ConfigSet, cb structs.ProgressCallback) (*DecryptionContext, error) {
	var err error
	var optstimeout uint
	optstimeout = 0
	timeout := time.Duration(optstimeout * uint(time.Millisecond))
	header := make(http.Header)

	// request media playlist
	req, err := http.NewRequest("GET", playlistUrl, nil)
	if err != nil {
		return nil, err
	}
	req.Header = header
	// requesting an HLS playlist should be relatively fast, so we set the timeout directly on the client
	do, err := (&http.Client{Timeout: timeout}).Do(req)
	if err != nil {
		return nil, err
	}

	// parse m3u8
	segments, err := parseMediaPlaylist(do.Body)
	if err != nil {
		return nil, err
	}
	segment := segments[0]
	if segment == nil {
		return nil, errors.New("no segments extracted from playlist")
	}
	if segment.Limit <= 0 {
		return nil, errors.New("non-byterange playlists are currently unsupported")
	}

	// get URL to the actual file
	parsedUrl, err := url.Parse(playlistUrl)
	if err != nil {
		return nil, err
	}
	fileUrl, err := parsedUrl.Parse(segment.URI)
	if err != nil {
		return nil, err
	}

	// request mp4
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	req, err = http.NewRequestWithContext(ctx, "GET", fileUrl.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header = header

	var body io.Reader
	client := &http.Client{Timeout: timeout}
	if optstimeout > 0 {
		// create the timer before calling Do so that the timeout covers TCP handshake,
		// TLS handshake, sending the request and receiving HTTP headers
		timer := time.AfterFunc(timeout, func() { cancel(ErrTimeout) })
		do, err = client.Do(req)
		if err != nil {
			return nil, err
		}
		defer do.Body.Close()
		body = &TimedResponseBody{
			timeout:   timeout,
			timer:     timer,
			threshold: 256,
			body:      do.Body,
		}
		// Always download to file first in this new flow
		ofh, err := os.Create(outfile)
		if err != nil {
			return nil, err
		}
		defer ofh.Close()

		var writer io.Writer
		if cb != nil {
			pw := &ProgressWriter{Total: do.ContentLength, Callback: cb, Msg: "Downloading"}
			writer = io.MultiWriter(ofh, pw)
		} else {
			bar := progressbar.NewOptions64(
				do.ContentLength,
				progressbar.OptionClearOnFinish(),
				progressbar.OptionSetElapsedTime(false),
				progressbar.OptionSetPredictTime(false),
				progressbar.OptionShowElapsedTimeOnFinish(),
				progressbar.OptionShowCount(),
				progressbar.OptionEnableColorCodes(true),
				progressbar.OptionShowBytes(true),
				progressbar.OptionSetDescription("Downloading..."),
				progressbar.OptionSetTheme(progressbar.Theme{
					Saucer:        "",
					SaucerHead:    "",
					SaucerPadding: "",
					BarStart:      "",
					BarEnd:        "",
				}),
			)
			writer = io.MultiWriter(ofh, bar)
		}

		if _, err := io.Copy(writer, body); err != nil {
			return nil, err
		}

		if cb == nil {
			fmt.Print("Downloaded\n")
		}
	} else {
		do, err = client.Do(req)
		if err != nil {
			return nil, err
		}
		defer do.Body.Close()

		// Always download to file first in this new flow
		ofh, err := os.Create(outfile)
		if err != nil {
			return nil, err
		}
		defer ofh.Close()

		var writer io.Writer
		if cb != nil {
			pw := &ProgressWriter{Total: do.ContentLength, Callback: cb, Msg: "Downloading"}
			writer = io.MultiWriter(ofh, pw)
		} else {
			bar := progressbar.NewOptions64(
				do.ContentLength,
				progressbar.OptionClearOnFinish(),
				progressbar.OptionSetElapsedTime(false),
				progressbar.OptionSetPredictTime(false),
				progressbar.OptionShowElapsedTimeOnFinish(),
				progressbar.OptionShowCount(),
				progressbar.OptionEnableColorCodes(true),
				progressbar.OptionShowBytes(true),
				progressbar.OptionSetDescription("Downloading..."),
				progressbar.OptionSetTheme(progressbar.Theme{
					Saucer:        "",
					SaucerHead:    "",
					SaucerPadding: "",
					BarStart:      "",
					BarEnd:        "",
				}),
			)
			writer = io.MultiWriter(ofh, bar)
		}

		if _, err := io.Copy(writer, do.Body); err != nil {
			return nil, err
		}

		if cb == nil {
			fmt.Print("Downloaded\n")
		}
	}

	return &DecryptionContext{
		AdamId:           adamId,
		PlaylistSegments: segments,
		TotalLen:         do.ContentLength, // This might be approximate if using TimedResponseBody, but for download it's fine.
		Config:           Config,
	}, nil
}

// Decrypt decrypts the file at tempFile using the context and writes to outFile.
// If tempFile and outFile are the same, it will read into memory or a temp file?
// Actually downloadAndDecryptFile reads from `in` and writes to `outfile`.
// If we pass the same file path, os.Create(outfile) will truncate it!
// So we must download to a temp file, and decrypt to the final file.
func Decrypt(dCtx *DecryptionContext, tempFile string, outFile string, cb structs.ProgressCallback) error {
	// Open the downloaded encrypted file
	f, err := os.Open(tempFile)
	if err != nil {
		return err
	}
	defer f.Close()

	// connect to decryptor
	addr := dCtx.Config.DecryptM3u8Port
	if cb != nil {
		cb(0.0, "Connecting to Decryptor", 0)
	}
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return err
	}
	defer Close(conn)

	// Call the internal function
	// Note: downloadAndDecryptFile expects `in` to be the stream.
	// It writes to `outfile`.
	return downloadAndDecryptFile(conn, f, outFile, dCtx.AdamId, dCtx.PlaylistSegments, dCtx.TotalLen, dCtx.Config, cb)
}

func downloadAndDecryptFile(conn io.ReadWriter, in io.Reader, outfile string,
	adamId string, playlistSegments []*m3u8.MediaSegment, totalLen int64, Config structs.ConfigSet, cb structs.ProgressCallback) error {
	if cb != nil {
		cb(0.0, "Decrypting (Init)", 0)
	}
	var buffer bytes.Buffer
	var outBuf *bufio.Writer
	MaxMemorySize := int64(Config.MaxMemoryLimit * 1024 * 1024)
	inBuf := bufio.NewReader(in)
	if totalLen <= MaxMemorySize {
		outBuf = bufio.NewWriter(&buffer)
	} else {
		ofh, err := os.Create(outfile)
		if err != nil {
			return err
		}
		defer ofh.Close()
		outBuf = bufio.NewWriter(ofh)
	}
	init, offset, err := ReadInitSegment(inBuf)
	if err != nil {
		return err
	}
	if init == nil {
		return errors.New("no init segment found")
	}

	tracks, err := TransformInit(init)
	if err != nil {
		return err
	}
	err = sanitizeInit(init)
	if err != nil {
		// errors returned by sanitizeInit are non-fatal
		fmt.Printf("Warning: unable to sanitize init completely: %s\n", err)
	}
	err = init.Encode(outBuf)
	if err != nil {
		return err
	}

	// 'segment' in m3u8 == 'fragment' in mp4ff
	//fmt.Println("Starting decryption...")
	var bar *progressbar.ProgressBar
	var pw *ProgressWriter

	if cb != nil {
		pw = &ProgressWriter{Total: totalLen, Current: int64(offset), Callback: cb, Msg: "Decrypting"}
	} else {
		bar = progressbar.NewOptions64(totalLen,
			progressbar.OptionClearOnFinish(),
			progressbar.OptionSetElapsedTime(false),
			progressbar.OptionSetPredictTime(false),
			progressbar.OptionShowElapsedTimeOnFinish(),
			progressbar.OptionShowCount(),
			progressbar.OptionEnableColorCodes(true),
			progressbar.OptionShowBytes(true),
			progressbar.OptionSetDescription("Decrypting..."),
			progressbar.OptionSetTheme(progressbar.Theme{
				Saucer:        "",
				SaucerHead:    "",
				SaucerPadding: "",
				BarStart:      "",
				BarEnd:        "",
			}),
		)
		bar.Add64(int64(offset))
	}
	rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
	for i := 0; ; i++ {
		var frag *mp4.Fragment
		rawoffset := offset
		frag, offset, err = ReadNextFragment(inBuf, offset)
		rawoffset = offset - rawoffset
		if err != nil {
			return err
		}
		if frag == nil {
			// check offset against Content-Length?
			break
		}
		// print progress

		// if totalLen > 0 {
		// 	fmt.Printf("%.2f%% of %d bytes\n", 100*float32(offset)/float32(totalLen), totalLen)
		// }
		segment := playlistSegments[i]
		if segment == nil {
			return errors.New("segment number out of sync")
		}
		key := segment.Key
		if key != nil {
			if i != 0 {
				SwitchKeys(rw)
			}
			if key.URI == prefetchKey {
				SendString(rw, "0")
			} else {
				SendString(rw, adamId)
			}
			SendString(rw, key.URI)
		}
		// flushes the buffer
		err = DecryptFragment(frag, tracks, rw)
		if err != nil {
			return fmt.Errorf("decryptFragment: %w", err)
		}
		err = frag.Encode(outBuf)
		if err != nil {
			return err
		}
		if cb != nil {
			pw.Update(int(rawoffset))
		} else {
			bar.Add64(int64(rawoffset))
		}
	}
	err = outBuf.Flush()
	if err != nil {
		return err
	}
	if totalLen <= MaxMemorySize {
		// create output file
		ofh, err := os.Create(outfile)
		if err != nil {
			return err
		}
		defer ofh.Close()

		_, err = ofh.Write(buffer.Bytes())
		if err != nil {
			return err
		}
	}
	return nil
}

// Remove boxes in the init segment that are known to cause compatibility issues
func sanitizeInit(init *mp4.InitSegment) error {
	traks := init.Moov.Traks
	if len(traks) > 1 {
		return errors.New("more than 1 track found")
	}
	// Remove duplicate ec-3 or alac boxes in stsd since some programs (e.g. cuetools) don't
	// like it when there's more than 1 entry in stsd.
	// Every audio track contains two of these boxes because two IVs are needed to decrypt the
	// track. The two boxes become identical after removing encryption info.
	stsd := traks[0].Mdia.Minf.Stbl.Stsd
	if stsd.SampleCount == 1 {
		return nil
	}
	if stsd.SampleCount > 2 {
		return fmt.Errorf("expected only 1 or 2 entries in stsd, got %d", stsd.SampleCount)
	}
	children := stsd.Children
	if children[0].Type() != children[1].Type() {
		return errors.New("children in stsd are not of the same type")
	}
	stsd.Children = children[:1]
	stsd.SampleCount = 1
	return nil
}

// Workaround for m3u8 not supporting multiple keys - remove
// PlayReady and Widevine
func filterResponse(f io.Reader) (*bytes.Buffer, error) {
	buf := &bytes.Buffer{}
	scanner := bufio.NewScanner(f)

	prefix := []byte("#EXT-X-KEY:")
	keyFormat := []byte("streamingkeydelivery")
	for scanner.Scan() {
		lineBytes := scanner.Bytes()
		if bytes.HasPrefix(lineBytes, prefix) && !bytes.Contains(lineBytes, keyFormat) {
			continue
		}
		_, err := buf.Write(lineBytes)
		if err != nil {
			return nil, err
		}
		_, err = buf.WriteString("\n")
		if err != nil {
			return nil, err
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return buf, nil
}

func parseMediaPlaylist(r io.ReadCloser) ([]*m3u8.MediaSegment, error) {
	defer r.Close()
	playlistBuf, err := filterResponse(r)
	if err != nil {
		return nil, err
	}

	playlist, listType, err := m3u8.Decode(*playlistBuf, true)
	if err != nil {
		return nil, err
	}

	if listType != m3u8.MEDIA {
		return nil, errors.New("m3u8 not of media type")
	}

	mediaPlaylist := playlist.(*m3u8.MediaPlaylist)
	return mediaPlaylist.Segments, nil
}

// pasing
func ReadInitSegment(r io.Reader) (*mp4.InitSegment, uint64, error) {
	var offset uint64 = 0
	init := mp4.NewMP4Init()
	for i := 0; i < 2; i++ {
		box, err := mp4.DecodeBox(offset, r)
		if err != nil {
			return nil, offset, err
		}
		boxType := box.Type()
		if boxType != "ftyp" && boxType != "moov" {
			return nil, offset, fmt.Errorf("unexpected box type %s, should be ftyp or moov", boxType)
		}
		init.AddChild(box)
		offset += box.Size()
	}
	return init, offset, nil
}

// Get the next fragment. Returns nil and no error on EOF
func ReadNextFragment(r io.Reader, offset uint64) (*mp4.Fragment, uint64, error) {
	frag := mp4.NewFragment()
	for {
		box, err := mp4.DecodeBox(offset, r)
		if err == io.EOF {
			return nil, offset, nil
		}
		if err != nil {
			return nil, offset, err
		}
		boxType := box.Type()
		// fmt.Printf("processing %s, box starts @ offset %d\n", boxType, offset)
		offset += box.Size()
		if boxType == "moof" || boxType == "emsg" || boxType == "prft" {
			frag.AddChild(box)
			continue
		}
		if boxType == "mdat" {
			frag.AddChild(box)
			break
		}
		fmt.Printf("ignoring a %s box found mid-stream", boxType)
	}
	// only 1 mdat box in fragment, meaning that the box doesn't have a preceding moof box
	if frag.Moof == nil {
		return nil, offset, fmt.Errorf("more than one mdat box in fragment (box ends @ offset %d)", offset)
	}
	return frag, offset, nil
}

// Return a new slice of boxes with encryption-related sbgp and sgpd removed,
// and the total number of bytes removed.
// Non-encryption-related ones such as 'roll' are left untouched.
func FilterSbgpSgpd(children []mp4.Box) ([]mp4.Box, uint64) {
	var bytesRemoved uint64 = 0
	remainingChildren := make([]mp4.Box, 0, len(children))
	for _, child := range children {
		switch box := child.(type) {
		case *mp4.SbgpBox:
			if box.GroupingType == "seam" || box.GroupingType == "seig" {
				bytesRemoved += child.Size()
				continue
			}
		case *mp4.SgpdBox:
			if box.GroupingType == "seam" || box.GroupingType == "seig" {
				bytesRemoved += child.Size()
				continue
			}
		}
		remainingChildren = append(remainingChildren, child)
	}
	return remainingChildren, bytesRemoved
}

// Get decryption info for tracks from init segment and remove encryption-related boxes
func TransformInit(init *mp4.InitSegment) (map[uint32]mp4.DecryptTrackInfo, error) {
	di, err := mp4.DecryptInit(init)
	tracks := make(map[uint32]mp4.DecryptTrackInfo, len(di.TrackInfos))
	for _, ti := range di.TrackInfos {
		tracks[ti.TrackID] = ti
	}
	if err != nil {
		return tracks, err
	}
	// remove encryption-related sbgp and sgpd
	for _, trak := range init.Moov.Traks {
		stbl := trak.Mdia.Minf.Stbl
		stbl.Children, _ = FilterSbgpSgpd(stbl.Children)
	}
	return tracks, nil
}

// remote
// Reset the loops on the script's end and close the connection
func Close(conn io.WriteCloser) error {
	defer conn.Close()
	_, err := conn.Write([]byte{0, 0, 0, 0, 0})
	return err
}

func SwitchKeys(conn io.Writer) error {
	_, err := conn.Write([]byte{0, 0, 0, 0})
	return err
}

// Send id or keyUri
func SendString(conn io.Writer, uri string) error {
	_, err := conn.Write([]byte{byte(len(uri))})
	if err != nil {
		return err
	}
	_, err = io.WriteString(conn, uri)
	return err
}

func cbcsFullSubsampleDecrypt(data []byte, conn *bufio.ReadWriter) error {
	// Drops 4 last bits -> multiple of 16
	// It wouldn't hurt to send the remaining bytes also because the decryption
	// function would just return them as-is, but we're truncating the data here
	// for clarity and interoperability
	truncatedLen := len(data) & ^0xf
	// send the whole chunk at once
	err := binary.Write(conn, binary.LittleEndian, uint32(truncatedLen))
	if err != nil {
		return err
	}
	_, err = conn.Write(data[:truncatedLen])
	if err != nil {
		return err
	}
	err = conn.Flush()
	if err != nil {
		return err
	}
	_, err = io.ReadFull(conn, data[:truncatedLen])
	return err
}

func cbcsStripeDecrypt(data []byte, conn *bufio.ReadWriter, decryptBlockLen, skipBlockLen int) error {
	size := len(data)

	// block too small, ignore
	if size < decryptBlockLen {
		return nil
	}

	// number of encrypted blocks in this sample
	count := ((size - decryptBlockLen) / (decryptBlockLen + skipBlockLen)) + 1
	totalLen := count * decryptBlockLen

	err := binary.Write(conn, binary.LittleEndian, uint32(totalLen))
	if err != nil {
		return err
	}

	pos := 0
	for {
		if size-pos < decryptBlockLen { // Leave the rest
			break
		}
		_, err = conn.Write(data[pos : pos+decryptBlockLen])
		if err != nil {
			return err
		}
		pos += decryptBlockLen
		if size-pos < skipBlockLen {
			break
		}
		pos += skipBlockLen
	}
	err = conn.Flush()
	if err != nil {
		return err
	}

	pos = 0
	for {
		if size-pos < decryptBlockLen {
			break
		}
		_, err = io.ReadFull(conn, data[pos:pos+decryptBlockLen])
		if err != nil {
			return err
		}
		pos += decryptBlockLen
		if size-pos < skipBlockLen {
			break
		}
		pos += skipBlockLen
	}
	return nil
}

// Decryption function dispatcher
func cbcsDecryptRaw(data []byte, conn *bufio.ReadWriter, decryptBlockLen, skipBlockLen int) error {
	if skipBlockLen == 0 {
		// Full encryption of subsamples
		// e.g. Apple Music ALAC
		return cbcsFullSubsampleDecrypt(data, conn)
	} else {
		// Pattern (stripe) encryption of subsamples
		// e.g. most AVC and HEVC applications
		return cbcsStripeDecrypt(data, conn, decryptBlockLen, skipBlockLen)
	}
}

// Decrypt a cbcs-encrypted sample in-place
func cbcsDecryptSample(sample []byte, conn *bufio.ReadWriter,
	subSamplePatterns []mp4.SubSamplePattern, tenc *mp4.TencBox) error {

	decryptBlockLen := int(tenc.DefaultCryptByteBlock) * 16
	skipBlockLen := int(tenc.DefaultSkipByteBlock) * 16
	var pos uint32 = 0

	// Full sample encryption
	if len(subSamplePatterns) == 0 {
		return cbcsDecryptRaw(sample, conn, decryptBlockLen, skipBlockLen)
	}

	// Has subsamples
	for j := 0; j < len(subSamplePatterns); j++ {
		ss := subSamplePatterns[j]
		pos += uint32(ss.BytesOfClearData)

		// Nothing to decrypt!
		if ss.BytesOfProtectedData <= 0 {
			continue
		}

		err := cbcsDecryptRaw(sample[pos:pos+ss.BytesOfProtectedData],
			conn, decryptBlockLen, skipBlockLen)
		if err != nil {
			return err
		}
		pos += ss.BytesOfProtectedData
	}

	return nil
}

// Decrypt an array of cbcs-encrypted samples in-place
func cbcsDecryptSamples(samples []mp4.FullSample, conn *bufio.ReadWriter,
	tenc *mp4.TencBox, senc *mp4.SencBox) error {

	for i := range samples {
		var subSamplePatterns []mp4.SubSamplePattern
		if len(senc.SubSamples) != 0 {
			subSamplePatterns = senc.SubSamples[i]
		}
		err := cbcsDecryptSample(samples[i].Data, conn, subSamplePatterns, tenc)
		if err != nil {
			return err
		}
	}
	return nil
}

func DecryptFragment(frag *mp4.Fragment, tracks map[uint32]mp4.DecryptTrackInfo, conn *bufio.ReadWriter) error {
	moof := frag.Moof
	var bytesRemoved uint64 = 0

	for _, traf := range moof.Trafs {
		ti, ok := tracks[traf.Tfhd.TrackID]
		if !ok {
			return fmt.Errorf("could not find decryption info for track %d", traf.Tfhd.TrackID)
		}
		if ti.Sinf == nil {
			// unencrypted track
			continue
		}

		schemeType := ti.Sinf.Schm.SchemeType
		if schemeType != "cbcs" {
			return fmt.Errorf("scheme type %s not supported", schemeType)
		}
		hasSenc, isParsed := traf.ContainsSencBox()
		if !hasSenc {
			return fmt.Errorf("no senc box in traf")
		}

		var senc *mp4.SencBox
		if traf.Senc != nil {
			senc = traf.Senc
		} else {
			senc = traf.UUIDSenc.Senc
		}

		if !isParsed {
			// simply ignore sbgp and sgpd
			// "Sample To Group Box ('sbgp') and Sample Group Description Box ('sgpd')
			// of type 'seig' are used to indicate the KID applied to each sample, and changes
			// to KIDs over time (i.e. 'key rotation')"
			// (ref: https://dashif.org/docs/DASH-IF-IOP-v3.2.pdf)
			err := senc.ParseReadBox(ti.Sinf.Schi.Tenc.DefaultPerSampleIVSize, traf.Saiz)
			if err != nil {
				return err
			}
		}

		samples, err := frag.GetFullSamples(ti.Trex)
		if err != nil {
			return err
		}

		err = cbcsDecryptSamples(samples, conn, ti.Sinf.Schi.Tenc, senc)
		if err != nil {
			return err
		}

		bytesRemoved += traf.RemoveEncryptionBoxes()
	}
	_, psshBytesRemoved := moof.RemovePsshs()
	bytesRemoved += psshBytesRemoved
	for _, traf := range moof.Trafs {
		for _, trun := range traf.Truns {
			trun.DataOffset -= int32(bytesRemoved)
		}
	}

	return nil
}
