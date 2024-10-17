package downloader

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cheggaaa/pb/v3"
	"github.com/grafov/m3u8"
	log "github.com/sirupsen/logrus"
	"github.com/weaming/m3u8-downloader/decrypter"
	"github.com/weaming/m3u8-downloader/utils"
)

var (
	tmpl       = `{{ "Downloading:" | rndcolor }} {{string . "prefix" | rndcolor }}{{counters . | rndcolor }} {{bar . | rndcolor }} {{percent . | rndcolor }} {{speed . | rndcolor }} {{rtime . "ETA %s"| rndcolor }}{{string . "suffix"| rndcolor }}`
	syncByte   = uint8(71) //0x47
	outputPath string
)

var (
	downloadUrl    string
	output         string
	downloadDir    string
	proxy          string
	deleteSyncByte bool
	deleteTS       bool
	threadNumber   int
	headers        []string
	cdns           []string
	baseUrl        string
	keyStr         string
	keyFormat      string
	useFFmpeg      bool
	liveStream     float64
)

// Options defines common m3u8 options.
type Options struct {
	DownloadUrl    string
	Output         string
	DownloadDir    string
	Proxy          string
	DeleteSyncByte bool
	DeleteTS       bool
	ThreadNumber   int
	Headers        []string
	CDNs           []string
	BaseUrl        string
	Key            string
	KeyFormat      string
	UseFFmpeg      bool
	LiveStream     float64
}

// SetOptions sets the common request option.
func SetOptions(opt Options) {
	downloadUrl = opt.DownloadUrl
	output = opt.Output
	downloadDir = ResolveDir(opt.DownloadDir)
	proxy = opt.Proxy
	deleteSyncByte = opt.DeleteSyncByte
	deleteTS = opt.DeleteTS
	threadNumber = opt.ThreadNumber
	headers = opt.Headers
	cdns = opt.CDNs
	baseUrl = opt.BaseUrl
	keyStr = opt.Key
	keyFormat = opt.KeyFormat
	useFFmpeg = opt.UseFFmpeg
	liveStream = opt.LiveStream
}

func ResolveDir(dirStr string) string {
	abs := filepath.IsAbs(dirStr)
	if abs {
		log.Trace("Resolve download directory:" + dirStr)
		return dirStr
	}
	pwd, _ := os.Getwd()
	dir, err := filepath.Abs(filepath.Join(pwd, dirStr))

	if err != nil {
		log.Error("Resolve download directory failed")
		return dirStr
	}

	log.Trace("Resolve download directory:" + dir)
	return dir
}

func Download() {
	enablePipeline := EnvInt64("ENABLE_PIPELINE", 1) > 0
	if enablePipeline && liveStream > 0 {
		log.Info("✓ Using pipeline mode for live stream")
		DownloadPipeline()
		return
	}

	log.Debug("Using sequential mode")
	initCDN(cdns)
	initHttpClient(proxy, headers)
	checkOutputFolder()

	minLoopWait := EnvInt64("MIN_LOOP_WAIT", 0)
	maxLoopWait := EnvInt64("MAX_LOOP_WAIT", 10)
	defaultLoopWait := EnvInt64("LOOP_WAIT", 0)

	segmentsSeen := map[string]bool{}
	var totalDuration float64
	var lastFetchTime time.Time
	var lastMediaSequence uint64
	var totalMissingSegments int

	progressPath := ""
	enableResume := EnvInt64("ENABLE_RESUME", 0) > 0
	if enableResume && liveStream > 0 {
		progressPath = filepath.Join(downloadDir, output+"_progress.json")
		if savedProgress, err := loadProgress(progressPath); err == nil {
			if savedProgress.DownloadURL == downloadUrl {
				segmentsSeen = savedProgress.SegmentsSeen
				totalDuration = savedProgress.TotalDuration
				lastMediaSequence = savedProgress.LastMediaSequence
				totalMissingSegments = savedProgress.MissingSegments
				log.Infof("✓ Resumed from previous session:")
				log.Infof("  Last sequence: %d", lastMediaSequence)
				log.Infof("  Total duration: %.1fs", totalDuration)
				log.Infof("  Segments seen: %d", len(segmentsSeen))
				log.Infof("  Missing segments: %d", totalMissingSegments)
			} else {
				log.Warn("Progress file URL mismatch, starting fresh")
			}
		} else {
			log.Debug("No previous progress found, starting fresh")
		}
	}

	for index := 1; ; index++ {
		loopStartTime := time.Now()

		var data []byte
		var err error
		if strings.HasPrefix(downloadUrl, "http") {
			data, err = download(downloadUrl)
			if err != nil {
				log.Error("Download m3u8 failed:" + downloadUrl + ",Error:" + err.Error())
				return
			}
		} else {
			data, err = os.ReadFile(downloadUrl)
			if err != nil {
				log.Error("Read m3u8 file failed:" + downloadUrl + ",Error:" + err.Error())
				return
			}
			if len(baseUrl) == 0 {
				log.Warn("make sure ts file have full path in the m3u8 file")
			}
		}

		mpl, err := parseM3u8(data, downloadUrl)
		if err != nil {
			log.Error("Parse m3u8 file failed:" + err.Error())
			return
		} else {
			log.Info("Parse m3u8 file successfully")
		}

		if liveStream > 0 && mpl.SeqNo > 0 {
			if lastMediaSequence > 0 {
				expectedSeq := lastMediaSequence + 1
				if mpl.SeqNo > expectedSeq {
					missing := int(mpl.SeqNo - expectedSeq)
					totalMissingSegments += missing
					log.Errorf("⚠️  SEGMENT GAP DETECTED! Missing %d segments (sequence: %d -> %d)",
						missing, lastMediaSequence, mpl.SeqNo)
					log.Errorf("   Total missing segments: %d", totalMissingSegments)
				} else if mpl.SeqNo == lastMediaSequence {
					log.Debug("Same sequence number, playlist not updated yet")
				}
			}
			lastMediaSequence = mpl.SeqNo
		}

		var deduplicated []*m3u8.MediaSegment
		for _, seg := range mpl.Segments {
			if seg == nil {
				break
			}
			if _, ok := segmentsSeen[seg.URI]; !ok {
				deduplicated = append(deduplicated, seg)
				totalDuration += seg.Duration
				segmentsSeen[seg.URI] = true
			}
		}
		if len(deduplicated) < int(mpl.Count()) {
			log.Infof("Deduplicate %d segments", int(mpl.Count())-len(deduplicated))
		}
		mpl.Segments = deduplicated

		segmentDownloadStart := time.Now()
		downloadM3u8(mpl.Segments, mpl.Key, index)
		segmentDownloadDuration := time.Since(segmentDownloadStart)

		log.Infof("Downloaded: %d segments, %v", len(segmentsSeen), time.Duration(totalDuration*1000)*time.Millisecond)

		if liveStream > 0 && len(deduplicated) > 0 {
			avgTimePerSegment := segmentDownloadDuration.Seconds() / float64(len(deduplicated))
			segmentDuration := mpl.TargetDuration
			if segmentDuration == 0 && len(deduplicated) > 0 {
				segmentDuration = deduplicated[0].Duration
			}

			if avgTimePerSegment > segmentDuration {
				downloadSpeed := segmentDuration / avgTimePerSegment
				log.Warnf("⚠️  Download speed too slow! Speed ratio: %.2fx (need >1.0x)", downloadSpeed)
				log.Warnf("   Downloading %.1fs segment takes %.1fs", segmentDuration, avgTimePerSegment)
				log.Warnf("   Consider: increase threads (-n), use CDN (-C), or check network")

				if downloadSpeed < 0.5 {
					log.Errorf("❌ CRITICAL: Download speed <0.5x, high risk of missing segments!")
				}
			} else {
				downloadSpeed := segmentDuration / avgTimePerSegment
				log.Debugf("✓ Download speed OK: %.2fx", downloadSpeed)
			}
		}

		if liveStream == 0 {
			break
		} else if totalDuration > liveStream {
			break
		}

		waitDuration := calculateWaitDuration(
			mpl,
			defaultLoopWait,
			minLoopWait,
			maxLoopWait,
			len(deduplicated),
			loopStartTime,
			lastFetchTime,
		)
		lastFetchTime = time.Now()

		if enableResume && progressPath != "" && index%5 == 0 {
			progress := LiveStreamProgress{
				LastMediaSequence: lastMediaSequence,
				TotalDuration:     totalDuration,
				SegmentsSeen:      segmentsSeen,
				MissingSegments:   totalMissingSegments,
				LastUpdateTime:    time.Now(),
				DownloadURL:       downloadUrl,
			}
			if err := saveProgress(progressPath, progress); err != nil {
				log.Warnf("Failed to save progress: %v", err)
			} else {
				log.Debugf("Progress saved (sequence: %d, segments: %d)", lastMediaSequence, len(segmentsSeen))
			}
		}

		if waitDuration > 0 {
			log.Infof("Waiting %v before next fetch", waitDuration)
			time.Sleep(waitDuration)
		}
	}

	if enableResume && progressPath != "" {
		if err := os.Remove(progressPath); err != nil {
			log.Debugf("Failed to remove progress file: %v", err)
		} else {
			log.Debug("Progress file removed (download completed)")
		}
	}

	if totalMissingSegments > 0 {
		log.Errorf("⚠️  DOWNLOAD COMPLETED WITH %d MISSING SEGMENTS!", totalMissingSegments)
		log.Error("   The final video may have gaps or glitches")
	}

	temp_name := mergeFile()
	renameFile(temp_name)
}

func renameFile(temp_file string) {
	path1 := filepath.Join(outputPath, temp_file)
	path2 := filepath.Join(downloadDir, output)
	err := os.Rename(path1, path2)
	if err != nil {
		log.Println("[error] Rename failed: " + err.Error())
	}
	if deleteTS {
		err = os.RemoveAll(outputPath)
		if err != nil {
			log.Println(err)
		}
	}
}

func mergeFile() string {
	if !useFFmpeg {
		switch runtime.GOOS {
		case "windows":
			return utils.WinMergeFile(outputPath, deleteTS)
		default:
			return utils.UnixMergeFile(outputPath, deleteTS)
		}
	}
	return utils.FFmpegMergeFile(outputPath, deleteTS)
}

func checkOutputFolder() {
	log.Trace("Check output folder")
	if len(downloadDir) == 0 {
		return
	}

	outputPath = filepath.Join(downloadDir, output+"_downloading")
	log.Trace("Output path is : " + outputPath)
	utils.MkAllDir(outputPath)
}

func parseM3u8(data []byte, downloadUrl string) (*m3u8.MediaPlaylist, error) {
	log.Debug("Parse m3u8")
	playlist, listType, err := m3u8.Decode(*bytes.NewBuffer(data), false)
	if err != nil {
		log.Error("Decode m3u8 failed: " + err.Error())
		return nil, err
	}

	if listType == m3u8.MEDIA {
		var baseHost *url.URL
		if len(baseUrl) > 0 {
			baseHost, err = url.Parse(baseUrl)
			if err != nil {
				log.Error("url.Parse(" + baseUrl + ") failed: " + err.Error())
				return nil, errors.New("parse base url failed: " + err.Error())
			}
		} else if len(downloadUrl) > 0 {
			baseHost, err = url.Parse(downloadUrl)
			if err != nil {
				log.Error("url.Parse(" + downloadUrl + ") failed: " + err.Error())
				return nil, errors.New("parse m3u8 url failed: " + err.Error())
			}
		}
		log.Trace("Base host is " + baseHost.String())

		mpl := playlist.(*m3u8.MediaPlaylist)

		if mpl.Key != nil && mpl.Key.URI != "" {
			uri, err := formatURI(baseHost, mpl.Key.URI)
			if err != nil {
				log.Error("formatURI(" + mpl.Key.URI + ") failed: " + err.Error())
				return nil, err
			}
			log.Trace("MPL key URI is " + uri)
			mpl.Key.URI = uri
		}

		total := int(mpl.Count())
		for i := 0; i < total; i++ {
			segment := mpl.Segments[i]

			uri, err := formatURI(baseHost, segment.URI)
			if err != nil {
				log.Error("formatURI(" + segment.URI + ") failed: " + err.Error())
				return nil, err
			}
			log.Trace("Segment URI is " + uri)
			segment.URI = uri

			if segment.Key != nil && segment.Key.URI != "" {
				uri, err := formatURI(baseHost, segment.Key.URI)
				if err != nil {
					log.Error("formatURI(" + segment.Key.URI + ") failed: " + err.Error())
					return nil, err
				}
				log.Trace("Segment key URI is " + uri)
				segment.Key.URI = uri
			}
		}
		return mpl, nil
	}

	return nil, errors.New("unsupport m3u8 type")
}

func downloadM3u8(segments []*m3u8.MediaSegment, key *m3u8.Key, batch int) {
	var wg sync.WaitGroup
	threadLimiter := make(chan any, threadNumber)

	total := len(segments)
	if total == 0 {
		return
	}
	bar := pb.ProgressBarTemplate(tmpl).Start64(int64(total))

	for index, segment := range segments {
		wg.Add(1)
		threadLimiter <- 1
		go func(segmentIndex int, seg *m3u8.MediaSegment) {
			defer func() {
				bar.Increment()
				wg.Done()
				<-threadLimiter
				log.Trace("args ...interface{}")
			}()
			currentPath := fmt.Sprintf("%s/%05d-%05d.ts", outputPath, batch, segmentIndex)
			if utils.IsExist(currentPath) {
				log.Warn("File: " + currentPath + " already exist")
				return
			}
			var keyURL, ivStr string
			if seg.Key != nil && seg.Key.URI != "" {
				keyURL = seg.Key.URI
				ivStr = seg.Key.IV
			} else if key != nil && key.URI != "" {
				keyURL = key.URI
				ivStr = key.IV
			}
			log.Trace("keyURL is " + keyURL + ", ivStr is " + ivStr)

			// log.Info("segment: ", seg.URI)
			data, err := download(seg.URI)
			if err != nil {
				log.Error("Download : " + seg.URI + " failed: " + err.Error())
			}

			var originalData []byte

			if len(keyStr) > 0 {
				log.Info("Try to decrypt data by custom key " + keyStr)
				var key, iv []byte
				if ivStr != "" {
					iv, err = hex.DecodeString(strings.TrimPrefix(ivStr, "0x"))
					if err != nil {
						log.Error("Decode iv failed:" + err.Error())
					}
				} else {
					iv = createDefaultIV(segmentIndex)
				}
				switch strings.ToLower(keyFormat) {
				case "original":
					key = []byte(keyStr)
				case "hex":
					key = utils.HexDecode(keyStr)
				case "base64":
					var err error
					key, err = base64.StdEncoding.DecodeString(keyStr)
					if err != nil {
						log.Errorf("base64 Decode %s Failed: %s.", keyStr, err.Error())
					}
				default:
					key = []byte(keyStr)
				}

				originalData, err = decrypter.Decrypt(data, key, iv)
				if err != nil {
					log.Errorf("Decrypt failed by own key %s : %s", keyStr, err.Error())
				}
			} else if keyURL == "" {
				originalData = data
			} else {
				log.Info("Try to decrypt data")
				var key, iv []byte
				key, err = download(keyURL)
				if err != nil {
					log.Error("Download : " + keyURL + " failed: " + err.Error())
				}

				if ivStr != "" {
					iv, err = hex.DecodeString(strings.TrimPrefix(ivStr, "0x"))
					if err != nil {
						log.Error("Decode iv failed:" + err.Error())
					}
				} else {
					iv = createDefaultIV(segmentIndex)
				}
				originalData, err = decrypter.Decrypt(data, key, iv)
				if err != nil {
					log.Error("Decrypt failed:" + err.Error())
				}
			}

			if deleteSyncByte {
				log.Info("Delete sync byte.")
				// https://en.wikipedia.org/wiki/MPEG_transport_stream
				// Some TS files do not start with SyncByte 0x47, they can not be played after merging,
				// Need to remove the bytes before the SyncByte 0x47(71).
				dataLength := len(originalData)
				for j := 0; j < dataLength; j++ {
					if originalData[j] == syncByte {
						log.Warn("Find sync byte, and delete it.")
						originalData = originalData[j:]
						break
					}
				}
			}

			err = os.WriteFile(currentPath, originalData, 0666)
			if err != nil {
				log.Error("WriteFile failed:" + err.Error())
			}
			log.Trace("Save file '" + currentPath + "' successfully!")
		}(index, segment)
	}
	wg.Wait()
	bar.Finish()
}

func formatURI(base *url.URL, u string) (string, error) {
	if strings.HasPrefix(u, "http") {
		return u, nil
	}

	if base == nil {
		return "", errors.New("you must set m3u8 url for file to download")
	}

	obj, err := base.Parse(u)
	if err != nil {
		return "", err
	}

	return obj.String(), nil
}

func EnvInt64(key string, defaultValue int64) int64 {
	if value, ok := os.LookupEnv(key); ok {
		if v, err := strconv.ParseInt(value, 10, 64); err == nil {
			return v
		}
	}
	return defaultValue
}

func createDefaultIV(index int) []byte {
	iv := make([]byte, 16)
	binary.BigEndian.PutUint32(iv[12:], uint32(index))
	return iv
}

type LiveStreamProgress struct {
	LastMediaSequence uint64          `json:"last_media_sequence"`
	TotalDuration     float64         `json:"total_duration"`
	SegmentsSeen      map[string]bool `json:"segments_seen"`
	MissingSegments   int             `json:"missing_segments"`
	LastUpdateTime    time.Time       `json:"last_update_time"`
	DownloadURL       string          `json:"download_url"`
}

type SegmentTask struct {
	Segment      *m3u8.MediaSegment
	Key          *m3u8.Key
	BatchIndex   int
	SegmentIndex int
}

type DownloadStats struct {
	sync.Mutex
	TotalSegments     int
	MissingSegments   int
	TotalDuration     float64
	LastMediaSequence uint64
	SegmentsSeen      map[string]bool
}

func saveProgress(progressPath string, progress LiveStreamProgress) error {
	data, err := json.MarshalIndent(progress, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(progressPath, data, 0644)
}

func loadProgress(progressPath string) (*LiveStreamProgress, error) {
	data, err := os.ReadFile(progressPath)
	if err != nil {
		return nil, err
	}

	var progress LiveStreamProgress
	err = json.Unmarshal(data, &progress)
	if err != nil {
		return nil, err
	}

	return &progress, nil
}

func calculateWaitDuration(
	mpl *m3u8.MediaPlaylist,
	defaultWait int64,
	minWait int64,
	maxWait int64,
	newSegmentCount int,
	loopStartTime time.Time,
	lastFetchTime time.Time,
) time.Duration {
	if defaultWait > 0 {
		return time.Duration(defaultWait) * time.Second
	}

	var waitSeconds float64

	if mpl.TargetDuration > 0 {
		if newSegmentCount == 0 {
			waitSeconds = mpl.TargetDuration * 0.5
			log.Debugf("No new segments, using 50%% of target duration: %.1fs", waitSeconds)
		} else {
			waitSeconds = mpl.TargetDuration * 0.8
			log.Debugf("Found %d new segments, using 80%% of target duration: %.1fs", newSegmentCount, waitSeconds)
		}
	} else {
		segmentDurations := make([]float64, 0)
		for _, seg := range mpl.Segments {
			if seg != nil && seg.Duration > 0 {
				segmentDurations = append(segmentDurations, seg.Duration)
			}
		}

		if len(segmentDurations) > 0 {
			var avgDuration float64
			for _, d := range segmentDurations {
				avgDuration += d
			}
			avgDuration /= float64(len(segmentDurations))

			if newSegmentCount == 0 {
				waitSeconds = avgDuration * 0.5
				log.Debugf("No new segments, using 50%% of avg segment duration: %.1fs", waitSeconds)
			} else {
				waitSeconds = avgDuration * 0.8
				log.Debugf("Found %d new segments, using 80%% of avg segment duration: %.1fs", newSegmentCount, waitSeconds)
			}
		} else {
			waitSeconds = 2.0
			log.Debug("Cannot determine segment duration, using default 2s")
		}
	}

	elapsedSinceLoopStart := time.Since(loopStartTime).Seconds()
	if elapsedSinceLoopStart < waitSeconds {
		waitSeconds -= elapsedSinceLoopStart
	} else {
		waitSeconds = 0
	}

	if minWait > 0 && waitSeconds < float64(minWait) {
		log.Debugf("Wait time %.1fs < min %ds, using min", waitSeconds, minWait)
		waitSeconds = float64(minWait)
	}
	if maxWait > 0 && waitSeconds > float64(maxWait) {
		log.Debugf("Wait time %.1fs > max %ds, using max", waitSeconds, maxWait)
		waitSeconds = float64(maxWait)
	}

	return time.Duration(waitSeconds * float64(time.Second))
}
