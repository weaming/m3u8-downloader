package downloader

import (
	"testing"
	"time"

	"github.com/grafov/m3u8"
)

func TestCalculateWaitDuration(t *testing.T) {
	tests := []struct {
		name              string
		targetDuration    float64
		segmentDurations  []float64
		defaultWait       int64
		minWait           int64
		maxWait           int64
		newSegmentCount   int
		downloadTime      time.Duration
		expectedWaitRange [2]float64
	}{
		{
			name:              "使用TargetDuration且有新片段",
			targetDuration:    2.0,
			defaultWait:       0,
			minWait:           0,
			maxWait:           10,
			newSegmentCount:   3,
			downloadTime:      0,
			expectedWaitRange: [2]float64{1.5, 1.7}, // 2 × 0.8 = 1.6
		},
		{
			name:              "使用TargetDuration且无新片段",
			targetDuration:    2.0,
			defaultWait:       0,
			minWait:           0,
			maxWait:           10,
			newSegmentCount:   0,
			downloadTime:      0,
			expectedWaitRange: [2]float64{0.9, 1.1}, // 2 × 0.5 = 1.0
		},
		{
			name:              "补偿下载时间",
			targetDuration:    2.0,
			defaultWait:       0,
			minWait:           0,
			maxWait:           10,
			newSegmentCount:   2,
			downloadTime:      1 * time.Second,
			expectedWaitRange: [2]float64{0.5, 0.7}, // 1.6 - 1.0 = 0.6
		},
		{
			name:              "下载时间超过计算等待时间",
			targetDuration:    2.0,
			defaultWait:       0,
			minWait:           0,
			maxWait:           10,
			newSegmentCount:   2,
			downloadTime:      3 * time.Second,
			expectedWaitRange: [2]float64{0, 0.1}, // 1.6 - 3.0 = 0
		},
		{
			name:              "应用最小等待限制",
			targetDuration:    2.0,
			defaultWait:       0,
			minWait:           2,
			maxWait:           10,
			newSegmentCount:   2,
			downloadTime:      0,
			expectedWaitRange: [2]float64{2.0, 2.0}, // min=2覆盖1.6
		},
		{
			name:              "应用最大等待限制",
			targetDuration:    10.0,
			defaultWait:       0,
			minWait:           0,
			maxWait:           5,
			newSegmentCount:   2,
			downloadTime:      0,
			expectedWaitRange: [2]float64{5.0, 5.0}, // max=5覆盖8.0
		},
		{
			name:              "使用固定LOOP_WAIT",
			targetDuration:    2.0,
			defaultWait:       3,
			minWait:           0,
			maxWait:           10,
			newSegmentCount:   2,
			downloadTime:      0,
			expectedWaitRange: [2]float64{3.0, 3.0}, // 固定值
		},
		{
			name:              "使用平均片段时长（无TargetDuration）",
			targetDuration:    0,
			segmentDurations:  []float64{1.6, 1.6, 1.6},
			defaultWait:       0,
			minWait:           0,
			maxWait:           10,
			newSegmentCount:   2,
			downloadTime:      0,
			expectedWaitRange: [2]float64{1.2, 1.3}, // 1.6 × 0.8 = 1.28
		},
		{
			name:              "无法确定时长时使用默认值",
			targetDuration:    0,
			segmentDurations:  []float64{},
			defaultWait:       0,
			minWait:           0,
			maxWait:           10,
			newSegmentCount:   2,
			downloadTime:      0,
			expectedWaitRange: [2]float64{1.9, 2.1}, // 默认2秒
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mpl := &m3u8.MediaPlaylist{
				TargetDuration: tt.targetDuration,
			}

			if len(tt.segmentDurations) > 0 {
				for _, duration := range tt.segmentDurations {
					seg := &m3u8.MediaSegment{
						Duration: duration,
						URI:      "test.ts",
					}
					mpl.Segments = append(mpl.Segments, seg)
				}
			}

			loopStartTime := time.Now().Add(-tt.downloadTime)
			lastFetchTime := time.Time{}

			waitDuration := calculateWaitDuration(
				mpl,
				tt.defaultWait,
				tt.minWait,
				tt.maxWait,
				tt.newSegmentCount,
				loopStartTime,
				lastFetchTime,
			)

			actualSeconds := waitDuration.Seconds()
			if actualSeconds < tt.expectedWaitRange[0] || actualSeconds > tt.expectedWaitRange[1] {
				t.Errorf("Expected wait time in range [%.1f, %.1f]s, got %.2fs",
					tt.expectedWaitRange[0], tt.expectedWaitRange[1], actualSeconds)
			} else {
				t.Logf("✓ Wait time: %.2fs (expected range: [%.1f, %.1f]s)",
					actualSeconds, tt.expectedWaitRange[0], tt.expectedWaitRange[1])
			}
		})
	}
}

func TestLiveStreamScenario(t *testing.T) {
	t.Log("模拟真实直播流场景")

	mpl := &m3u8.MediaPlaylist{
		TargetDuration: 2.0,
	}

	for i := 0; i < 3; i++ {
		seg := &m3u8.MediaSegment{
			Duration: 1.6,
			URI:      "segment.ts",
		}
		mpl.Segments = append(mpl.Segments, seg)
	}

	scenarios := []struct {
		name             string
		newSegments      int
		downloadTime     time.Duration
		expectedBehavior string
	}{
		{
			name:             "第1轮：首次下载3个片段",
			newSegments:      3,
			downloadTime:     2 * time.Second,
			expectedBehavior: "下载耗时超过理论等待，应立即进入下一轮",
		},
		{
			name:             "第2轮：正常下载2个新片段",
			newSegments:      2,
			downloadTime:     1500 * time.Millisecond,
			expectedBehavior: "等待约0.1秒",
		},
		{
			name:             "第3轮：网络变慢",
			newSegments:      2,
			downloadTime:     3500 * time.Millisecond,
			expectedBehavior: "下载太慢，应立即重新请求",
		},
		{
			name:             "第4轮：直播暂停，无新片段",
			newSegments:      0,
			downloadTime:     200 * time.Millisecond,
			expectedBehavior: "等待约0.8秒（减半策略）",
		},
	}

	for i, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			loopStartTime := time.Now().Add(-scenario.downloadTime)

			waitDuration := calculateWaitDuration(
				mpl,
				0,  // defaultWait
				0,  // minWait
				10, // maxWait
				scenario.newSegments,
				loopStartTime,
				time.Time{},
			)

			t.Logf("第%d轮: %s", i+1, scenario.name)
			t.Logf("  新片段数: %d", scenario.newSegments)
			t.Logf("  下载耗时: %v", scenario.downloadTime)
			t.Logf("  计算等待: %v", waitDuration)
			t.Logf("  预期行为: %s", scenario.expectedBehavior)
			t.Logf("")
		})
	}
}
