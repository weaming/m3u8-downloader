# m3u8-downloader 使用文档

## 快速开始

### 基础用法

```bash
# 下载普通视频
./m3u8-downloader download 'https://example.com/video.m3u8'

# 录制直播流（1小时）
./m3u8-downloader download 'https://live.com/stream.m3u8' -L 3600

# 自定义输出文件名
./m3u8-downloader download 'https://example.com/video.m3u8' -o my_video.ts
```

## 主要命令

### 1. download - 下载视频

```bash
./m3u8-downloader download [URL] [选项]
```

**常用选项**：

| 选项 | 简写 | 说明 | 默认值 |
|------|------|------|--------|
| `--output` | `-o` | 输出文件名 | 时间戳.ts |
| `--downloadDir` | `-f` | 下载目录 | ./output |
| `--threadNumber` | `-n` | 并发线程数 | 10 |
| `--liveStream` | `-L` | 直播流时长(秒) | 0(不录制) |
| `--logLevel` | `-l` | 日志级别 | Info |
| `--proxy` | `-x` | 代理服务器 | 无 |
| `--cdn` | `-C` | CDN服务器 | 无 |
| `--deleteTS` | `-D` | 下载后删除TS文件 | true |

**示例**：

```bash
# 录制2小时直播，使用20个线程
./m3u8-downloader download 'https://live.com/stream.m3u8' \
  -L 7200 \
  -n 20 \
  -o live_recording.ts

# 使用代理下载
./m3u8-downloader download 'https://example.com/video.m3u8' \
  -x http://127.0.0.1:8080

# 从文件读取m3u8
./m3u8-downloader download '/path/to/playlist.m3u8' \
  -u 'https://example.com/'
```

### 2. ping - 测试CDN速度

```bash
# 测试多个IP的响应时间
./m3u8-downloader ping 1.1.1.1,8.8.8.8,9.9.9.9

# 输出为下载命令参数格式
./m3u8-downloader ping 1.1.1.1,8.8.8.8,9.9.9.9 \
  -d cdn.example.com \
  -o p
```

**输出示例**：
```bash
# 时间格式(-o t)
1.1.1.1  time: 23.96ms
8.8.8.8  time: 61.23ms
9.9.9.9  time: 225.71ms

# 参数格式(-o p)
-C 'cdn.example.com:1.1.1.1' -C 'cdn.example.com:8.8.8.8'
```

## 直播流录制（推荐配置）

### 基础配置

```bash
# 启用所有优化功能
export ENABLE_PIPELINE=1    # 流水线并发（默认启用）
export ENABLE_RESUME=1      # 断点续传
export MIN_LOOP_WAIT=0      # 最小等待时间
export MAX_LOOP_WAIT=2      # 最大等待时间

./m3u8-downloader download \
  'https://live.example.com/stream.m3u8' \
  -L 3600 \
  -n 15 \
  -o recording.ts \
  -l Info
```

### 使用CDN加速

```bash
# 1. 先找最快的CDN节点
./m3u8-downloader ping \
  1.1.1.1,8.8.8.8,114.114.114.114 \
  -d cdn.example.com \
  -o p

# 输出: -C 'cdn.example.com:1.1.1.1' -C 'cdn.example.com:8.8.8.8'

# 2. 使用CDN下载
./m3u8-downloader download \
  'https://cdn.example.com/stream.m3u8' \
  -L 3600 \
  -C 'cdn.example.com:1.1.1.1' \
  -C 'cdn.example.com:8.8.8.8' \
  -o recording.ts
```

### 从Chrome复制下载

```bash
# 1. 在Chrome开发者工具找到m3u8请求
# 2. 右键 -> Copy -> Copy as cURL
# 3. 替换 curl 为 ./m3u8-downloader download

./m3u8-downloader download 'https://example.com/stream.m3u8' \
  -H 'user-agent: Mozilla/5.0...' \
  -H 'referer: https://example.com' \
  -H 'cookie: session=...' \
  -L 3600
```

## 环境变量配置

### 智能等待机制

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `LOOP_WAIT` | 0 | 固定等待时间(秒)，>0时禁用智能计算 |
| `MIN_LOOP_WAIT` | 0 | 最小等待时间(秒) |
| `MAX_LOOP_WAIT` | 10 | 最大等待时间(秒) |

**推荐配置**：
```bash
# 默认（推荐）- 自动智能计算
# 不设置任何变量

# 低延迟直播
export MIN_LOOP_WAIT=0
export MAX_LOOP_WAIT=1

# 保守模式（节省带宽）
export MIN_LOOP_WAIT=2
export MAX_LOOP_WAIT=5
```

### 功能开关

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `ENABLE_PIPELINE` | 1 | 启用流水线并发模式 |
| `ENABLE_RESUME` | 0 | 启用断点续传 |

```bash
# 启用断点续传
export ENABLE_RESUME=1

# 禁用流水线模式（回退到串行）
export ENABLE_PIPELINE=0
```

## 日志级别

```bash
# Trace - 最详细
./m3u8-downloader download 'https://...' -l Trace

# Debug - 调试信息
./m3u8-downloader download 'https://...' -l Debug

# Info - 常规信息（推荐）
./m3u8-downloader download 'https://...' -l Info

# Warning - 仅警告
./m3u8-downloader download 'https://...' -l Warning

# Error - 仅错误
./m3u8-downloader download 'https://...' -l Error
```

## 常见场景

### 场景1：录制低延迟直播（片段<2秒）

```bash
export ENABLE_PIPELINE=1
export ENABLE_RESUME=1
export MIN_LOOP_WAIT=0
export MAX_LOOP_WAIT=2

./m3u8-downloader download \
  'https://live.com/stream.m3u8' \
  -L 3600 \
  -n 20 \
  -o recording.ts
```

### 场景2：下载加密视频

```bash
# 使用自定义密钥
./m3u8-downloader download 'https://example.com/video.m3u8' \
  --key 'your-encryption-key' \
  --keyFormat hex

# keyFormat 可选值: original, hex, base64
```

### 场景3：网络不稳定环境

```bash
# 启用断点续传，增加等待时间
export ENABLE_RESUME=1
export MIN_LOOP_WAIT=2
export MAX_LOOP_WAIT=10

./m3u8-downloader download \
  'https://unstable-stream.com/stream.m3u8' \
  -L 3600 \
  -n 5 \
  -o recording.ts
```

### 场景4：恢复中断的下载

```bash
# 如果之前启用了 ENABLE_RESUME=1
# 重新运行相同的命令即可自动恢复

export ENABLE_RESUME=1
./m3u8-downloader download \
  'https://live.com/stream.m3u8' \
  -L 3600 \
  -o recording.ts

# 程序会自动检测并恢复进度
# [INFO] ✓ Resumed from previous session:
# [INFO]   Last sequence: 5166
# [INFO]   Total duration: 299.2s
```

## 输出解读

### 正常输出

```bash
[INFO] ✓ Using pipeline mode for live stream
[INFO] Parse m3u8 file successfully
[INFO] ✓ Found 3 new segments (total: 3, queue: 3)
[INFO] ✓ Found 2 new segments (total: 5, queue: 2)
[INFO] Downloaded: 5 segments, 8.0s
[DEBUG] ✓ Download speed OK: 2.13x
[INFO] Waiting 100ms before next fetch
```

### 警告信息

```bash
# 下载速度慢
[WARN] ⚠️  Download speed too slow! Speed ratio: 0.65x (need >1.0x)
[WARN]    Downloading 1.6s segment takes 2.5s
[WARN]    Consider: increase threads (-n), use CDN (-C), or check network

# 解决方案：
# 1. 增加线程数: -n 20
# 2. 使用CDN加速
# 3. 检查网络连接
```

### 错误信息

```bash
# 序列号跳跃（遗漏片段）
[ERROR] ⚠️  SEGMENT GAP DETECTED! Missing 2 segments (sequence: 4979 -> 4982)
[ERROR]    Total missing segments: 2

# 下载完成时的总结
[ERROR] ⚠️  DOWNLOAD COMPLETED WITH 2 MISSING SEGMENTS!
[ERROR]    The final video may have gaps or glitches

# 原因：
# - 网络太慢，跟不上直播流更新
# - 等待时间设置不当
# 解决：增加线程、使用CDN、调整等待时间
```

## 故障排查

### 问题1: 下载速度太慢

```bash
# 检查点：
# 1. 增加并发线程
./m3u8-downloader download ... -n 20

# 2. 使用CDN
./m3u8-downloader ping IP1,IP2,IP3 -d domain -o p
./m3u8-downloader download ... -C 'domain:fastest-ip'

# 3. 检查网络
ping example.com
```

### 问题2: 频繁遗漏片段

```bash
# 减少等待时间
export MAX_LOOP_WAIT=1
./m3u8-downloader download ...

# 或查看Debug日志
./m3u8-downloader download ... -l Debug
```

### 问题3: 程序崩溃

```bash
# 启用断点续传
export ENABLE_RESUME=1
./m3u8-downloader download ...

# 重启后会自动恢复
```

### 问题4: 队列堆积

```bash
# 日志显示：
[INFO] ✓ Found 10 new segments (total: 100, queue: 45)

# 原因：下载速度 < 推流速度
# 解决：
export MAX_LOOP_WAIT=1  # 加快检测
./m3u8-downloader download ... -n 25  # 增加线程
```

## 性能调优

### 低延迟模式（片段<2秒）

```bash
export ENABLE_PIPELINE=1
export MIN_LOOP_WAIT=0
export MAX_LOOP_WAIT=1
./m3u8-downloader download ... -L 3600 -n 20
```

### 标准模式（片段2-10秒）

```bash
export ENABLE_PIPELINE=1
export MIN_LOOP_WAIT=1
export MAX_LOOP_WAIT=5
./m3u8-downloader download ... -L 3600 -n 15
```

### 长片段模式（片段>10秒）

```bash
export ENABLE_PIPELINE=1
export MIN_LOOP_WAIT=2
export MAX_LOOP_WAIT=15
./m3u8-downloader download ... -L 3600 -n 10
```

### 节省带宽模式

```bash
export MIN_LOOP_WAIT=3
export MAX_LOOP_WAIT=10
./m3u8-downloader download ... -L 3600 -n 5
```

## 完整示例

### 示例1: 完整的直播录制流程

```bash
#!/bin/bash

# 配置环境变量
export ENABLE_PIPELINE=1
export ENABLE_RESUME=1
export MIN_LOOP_WAIT=0
export MAX_LOOP_WAIT=2

# 直播流URL
STREAM_URL="https://live.example.com/stream.m3u8"

# 录制时长（秒）
DURATION=7200

# 输出文件
OUTPUT="live_$(date +%Y%m%d_%H%M%S).ts"

# 开始录制
./m3u8-downloader download "$STREAM_URL" \
  -L $DURATION \
  -n 15 \
  -o "$OUTPUT" \
  -l Info

echo "录制完成: $OUTPUT"
```

### 示例2: 使用最快CDN录制

```bash
#!/bin/bash

DOMAIN="cdn.example.com"
STREAM_URL="https://$DOMAIN/stream.m3u8"

# 测试CDN节点
echo "正在测试CDN节点..."
CDN_IPS="1.1.1.1,8.8.8.8,114.114.114.114"
CDN_PARAMS=$(./m3u8-downloader ping $CDN_IPS -d $DOMAIN -o p)

echo "最快的CDN节点: $CDN_PARAMS"

# 使用最快节点下载
export ENABLE_PIPELINE=1
export ENABLE_RESUME=1

./m3u8-downloader download "$STREAM_URL" \
  -L 3600 \
  $CDN_PARAMS \
  -n 20 \
  -o recording.ts
```

## 技巧和最佳实践

1. **直播录制始终启用断点续传**
   ```bash
   export ENABLE_RESUME=1
   ```

2. **使用Debug日志排查问题**
   ```bash
   -l Debug > debug.log 2>&1
   ```

3. **根据片段时长调整等待**
   - 片段<2秒: `MAX_LOOP_WAIT=2`
   - 片段2-5秒: `MAX_LOOP_WAIT=5`
   - 片段>5秒: `MAX_LOOP_WAIT=10`

4. **根据网络调整线程**
   - 带宽充足: `-n 20`
   - 带宽一般: `-n 10`
   - 带宽受限: `-n 5`

5. **监控队列深度**
   - 队列<5: 正常
   - 队列5-20: 考虑增加线程
   - 队列>20: 网络或性能问题

## 获取帮助

```bash
# 查看命令帮助
./m3u8-downloader download -h
./m3u8-downloader ping -h

# 查看版本
./m3u8-downloader version
```

## 总结

**推荐配置（适用于大多数情况）**：

```bash
export ENABLE_PIPELINE=1
export ENABLE_RESUME=1

./m3u8-downloader download \
  'YOUR_M3U8_URL' \
  -L 3600 \
  -n 15 \
  -o recording.ts \
  -l Info
```

这个配置可以：
- ✅ 零遗漏录制
- ✅ 自动适配片段时长
- ✅ 崩溃后自动恢复
- ✅ 实时监控状态
- ✅ 最佳性能平衡
