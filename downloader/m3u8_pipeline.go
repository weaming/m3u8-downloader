package downloader

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/cheggaaa/pb/v3"
	log "github.com/sirupsen/logrus"
	"github.com/weaming/m3u8-downloader/decrypter"
	"github.com/weaming/m3u8-downloader/utils"
)

func DownloadPipeline() {
	initCDN(cdns)
	initHttpClient(proxy, headers)
	checkOutputFolder()

	minLoopWait := EnvInt64("MIN_LOOP_WAIT", 0)
	maxLoopWait := EnvInt64("MAX_LOOP_WAIT", 10)
	defaultLoopWait := EnvInt64("LOOP_WAIT", 0)
	enablePipeline := EnvInt64("ENABLE_PIPELINE", 1) > 0

	if !enablePipeline || liveStream == 0 {
		log.Info("Pipeline mode disabled, using sequential mode")
		Download()
		return
	}

	log.Info("✓ Using pipeline mode for live stream download")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	segmentChan := make(chan SegmentTask, 100)
	stats := &DownloadStats{
		SegmentsSeen: make(map[string]bool),
	}

	progressPath := ""
	enableResume := EnvInt64("ENABLE_RESUME", 0) > 0
	if enableResume {
		progressPath = fmt.Sprintf("%s/%s_progress.json", downloadDir, output)
		if savedProgress, err := loadProgress(progressPath); err == nil {
			if savedProgress.DownloadURL == downloadUrl {
				stats.Lock()
				stats.SegmentsSeen = savedProgress.SegmentsSeen
				stats.TotalDuration = savedProgress.TotalDuration
				stats.LastMediaSequence = savedProgress.LastMediaSequence
				stats.MissingSegments = savedProgress.MissingSegments
				stats.Unlock()
				log.Infof("✓ Resumed from previous session: %d segments", len(savedProgress.SegmentsSeen))
			}
		}
	}

	var wg sync.WaitGroup

	wg.Add(1)
	go fetchSegments(ctx, &wg, segmentChan, stats, defaultLoopWait, minLoopWait, maxLoopWait)

	wg.Add(1)
	go downloadSegments(ctx, &wg, segmentChan, stats)

	if enableResume && progressPath != "" {
		wg.Add(1)
		go saveProgressPeriodically(ctx, &wg, progressPath, stats, downloadUrl)
	}

	targetDuration := time.Duration(liveStream * float64(time.Second))
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	startTime := time.Now()
	for {
		select {
		case <-ticker.C:
			stats.Lock()
			currentDuration := stats.TotalDuration
			stats.Unlock()

			if currentDuration >= liveStream {
				log.Infof("✓ Target duration reached: %.1fs / %.1fs", currentDuration, liveStream)
				cancel()
				goto DONE
			}

			elapsed := time.Since(startTime)
			if elapsed > targetDuration+30*time.Second {
				log.Warn("⚠️  Timeout waiting for target duration")
				cancel()
				goto DONE
			}
		case <-ctx.Done():
			goto DONE
		}
	}

DONE:
	log.Info("Waiting for all goroutines to finish...")
	wg.Wait()

	stats.Lock()
	totalSegments := stats.TotalSegments
	missingSegments := stats.MissingSegments
	totalDuration := stats.TotalDuration
	stats.Unlock()

	if enableResume && progressPath != "" {
		os.Remove(progressPath)
		log.Debug("Progress file removed")
	}

	if missingSegments > 0 {
		log.Errorf("⚠️  DOWNLOAD COMPLETED WITH %d MISSING SEGMENTS!", missingSegments)
		log.Error("   The final video may have gaps or glitches")
	} else {
		log.Infof("✓ Download completed successfully: %d segments, %.1fs", totalSegments, totalDuration)
	}

	tempName := mergeFile()
	renameFile(tempName)
}

func fetchSegments(
	ctx context.Context,
	wg *sync.WaitGroup,
	segmentChan chan<- SegmentTask,
	stats *DownloadStats,
	defaultLoopWait, minLoopWait, maxLoopWait int64,
) {
	defer wg.Done()
	defer close(segmentChan)

	var lastFetchTime time.Time
	batchIndex := 1

	for {
		select {
		case <-ctx.Done():
			log.Debug("Fetch goroutine: context cancelled")
			return
		default:
		}

		loopStartTime := time.Now()

		var data []byte
		var err error
		if len(downloadUrl) > 0 && downloadUrl[0] == 'h' {
			data, err = download(downloadUrl)
			if err != nil {
				log.Errorf("Download m3u8 failed: %v", err)
				time.Sleep(2 * time.Second)
				continue
			}
		} else {
			data, err = os.ReadFile(downloadUrl)
			if err != nil {
				log.Errorf("Read m3u8 file failed: %v", err)
				return
			}
		}

		mpl, err := parseM3u8(data, downloadUrl)
		if err != nil {
			log.Errorf("Parse m3u8 failed: %v", err)
			time.Sleep(2 * time.Second)
			continue
		}

		stats.Lock()
		lastSeq := stats.LastMediaSequence
		stats.Unlock()

		if mpl.SeqNo > 0 {
			if lastSeq > 0 && mpl.SeqNo > lastSeq+1 {
				missing := int(mpl.SeqNo - lastSeq - 1)
				stats.Lock()
				stats.MissingSegments += missing
				stats.Unlock()
				log.Errorf("⚠️  SEGMENT GAP DETECTED! Missing %d segments (seq: %d -> %d)",
					missing, lastSeq, mpl.SeqNo)
			}

			stats.Lock()
			stats.LastMediaSequence = mpl.SeqNo
			stats.Unlock()
		}

		newSegmentCount := 0
		for i, seg := range mpl.Segments {
			if seg == nil {
				break
			}

			stats.Lock()
			_, seen := stats.SegmentsSeen[seg.URI]
			if !seen {
				stats.SegmentsSeen[seg.URI] = true
				stats.TotalDuration += seg.Duration
				stats.TotalSegments++
				newSegmentCount++
			}
			stats.Unlock()

			if !seen {
				task := SegmentTask{
					Segment:      seg,
					Key:          mpl.Key,
					BatchIndex:   batchIndex,
					SegmentIndex: i,
				}

				select {
				case segmentChan <- task:
					log.Debugf("→ Queued segment: %s", seg.URI)
				case <-ctx.Done():
					return
				}
			}
		}

		if newSegmentCount > 0 {
			log.Infof("✓ Found %d new segments (total: %d, queue: %d)",
				newSegmentCount, stats.TotalSegments, len(segmentChan))
		} else {
			log.Debug("No new segments found")
		}

		waitDuration := calculateWaitDuration(
			mpl,
			defaultLoopWait,
			minLoopWait,
			maxLoopWait,
			newSegmentCount,
			loopStartTime,
			lastFetchTime,
		)
		lastFetchTime = time.Now()

		if waitDuration > 0 {
			log.Debugf("Fetcher waiting %v before next check", waitDuration)
			select {
			case <-time.After(waitDuration):
			case <-ctx.Done():
				return
			}
		}

		batchIndex++
	}
}

func downloadSegments(
	ctx context.Context,
	wg *sync.WaitGroup,
	segmentChan <-chan SegmentTask,
	stats *DownloadStats,
) {
	defer wg.Done()

	var downloadWg sync.WaitGroup
	limiter := make(chan struct{}, threadNumber)

	bar := pb.ProgressBarTemplate(tmpl).Start(0)
	defer bar.Finish()

	downloadedCount := 0

	for {
		select {
		case task, ok := <-segmentChan:
			if !ok {
				log.Debug("Download goroutine: channel closed, waiting for workers")
				downloadWg.Wait()
				return
			}

			downloadWg.Add(1)
			limiter <- struct{}{}

			go func(t SegmentTask) {
				defer func() {
					downloadWg.Done()
					<-limiter
				}()

				downloadSingleSegment(t)

				downloadedCount++
				bar.SetTotal(int64(stats.TotalSegments))
				bar.SetCurrent(int64(downloadedCount))
			}(task)

		case <-ctx.Done():
			log.Debug("Download goroutine: context cancelled, waiting for workers")
			downloadWg.Wait()
			return
		}
	}
}

func downloadSingleSegment(task SegmentTask) {
	currentPath := fmt.Sprintf("%s/%05d-%05d.ts", outputPath, task.BatchIndex, task.SegmentIndex)

	if utils.IsExist(currentPath) {
		log.Debugf("✓ Segment already exists: %s", currentPath)
		return
	}

	seg := task.Segment
	key := task.Key

	var keyURL, ivStr string
	if seg.Key != nil && seg.Key.URI != "" {
		keyURL = seg.Key.URI
		ivStr = seg.Key.IV
	} else if key != nil && key.URI != "" {
		keyURL = key.URI
		ivStr = key.IV
	}

	data, err := download(seg.URI)
	if err != nil {
		log.Errorf("Download segment failed: %s, error: %v", seg.URI, err)
		return
	}

	originalData, err := decryptSegment(data, keyURL, ivStr, task.SegmentIndex)
	if err != nil {
		log.Errorf("Decrypt segment failed: %v", err)
		return
	}

	if deleteSyncByte {
		originalData = removeSyncByte(originalData)
	}

	err = os.WriteFile(currentPath, originalData, 0666)
	if err != nil {
		log.Errorf("Write file failed: %s, error: %v", currentPath, err)
		return
	}

	log.Debugf("✓ Downloaded: %s", currentPath)
}

func decryptSegment(data []byte, keyURL, ivStr string, segmentIndex int) ([]byte, error) {
	if len(keyStr) > 0 {
		var key, iv []byte
		var err error

		if ivStr != "" {
			iv, err = hex.DecodeString(strings.TrimPrefix(ivStr, "0x"))
			if err != nil {
				return nil, fmt.Errorf("decode iv failed: %w", err)
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
			key, err = base64.StdEncoding.DecodeString(keyStr)
			if err != nil {
				return nil, fmt.Errorf("base64 decode key failed: %w", err)
			}
		default:
			key = []byte(keyStr)
		}

		return decrypter.Decrypt(data, key, iv)
	} else if keyURL == "" {
		return data, nil
	} else {
		var key, iv []byte
		var err error

		key, err = download(keyURL)
		if err != nil {
			return nil, fmt.Errorf("download key failed: %w", err)
		}

		if ivStr != "" {
			iv, err = hex.DecodeString(strings.TrimPrefix(ivStr, "0x"))
			if err != nil {
				return nil, fmt.Errorf("decode iv failed: %w", err)
			}
		} else {
			iv = createDefaultIV(segmentIndex)
		}

		return decrypter.Decrypt(data, key, iv)
	}
}

func removeSyncByte(data []byte) []byte {
	dataLength := len(data)
	for j := 0; j < dataLength; j++ {
		if data[j] == syncByte {
			return data[j:]
		}
	}
	return data
}

func saveProgressPeriodically(
	ctx context.Context,
	wg *sync.WaitGroup,
	progressPath string,
	stats *DownloadStats,
	url string,
) {
	defer wg.Done()

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			stats.Lock()
			progress := LiveStreamProgress{
				LastMediaSequence: stats.LastMediaSequence,
				TotalDuration:     stats.TotalDuration,
				SegmentsSeen:      make(map[string]bool, len(stats.SegmentsSeen)),
				MissingSegments:   stats.MissingSegments,
				LastUpdateTime:    time.Now(),
				DownloadURL:       url,
			}
			for k, v := range stats.SegmentsSeen {
				progress.SegmentsSeen[k] = v
			}
			stats.Unlock()

			if err := saveProgress(progressPath, progress); err != nil {
				log.Warnf("Failed to save progress: %v", err)
			} else {
				log.Debugf("Progress saved: %d segments", len(progress.SegmentsSeen))
			}

		case <-ctx.Done():
			log.Debug("Progress saver goroutine: context cancelled")
			return
		}
	}
}
