# mudl-go

A lightweight command-line HTTP downloader focused on dynamic multi-threaded range scheduling.

## Features

- **NDM-style dynamic range scheduling**: workers use open-ended HTTP Range requests and dynamically split unfinished tails from active segments.
- **Tail stealing**: when no queued work remains, idle workers split work from the largest active remaining range.
- **Fresh URL resume**: resume an expired download by running the same output path with a new URL.
- **Filename detection**: prefers `Content-Disposition`, then falls back to the URL basename or `-o`.
- **Configurable User-Agent**: defaults to `mudl`, and can be changed with `-ua`.
- **CLI progress modes**: the default progress display is a single updating summary line; per-thread details are available with `-progress details`.

## Build

```powershell
go build -buildvcs=false -o mudl-go.exe .
```

## Usage

```powershell
.\mudl-go.exe "https://example.com/big-file.zip"
```

Common options:

```powershell
.\mudl-go.exe -c 32 -ua pan.baidu.com -min-chunk 8MB -max-chunk 128MB "URL"
```

Use quotes around URLs that contain `&`, especially signed URLs:

```powershell
.\mudl-go.exe -ua pan.baidu.com "https://example.com/file.zip?sign=abc&t=123&x-oss-expires=456"
```

Options:

| Option | Description |
| --- | --- |
| `-o` | Output file path |
| `-c` | Worker count |
| `-min-chunk` | Minimum dynamic range size |
| `-max-chunk` | Maximum dynamic range size |
| `-reserve` | Reserved window size kept for compatibility; dynamic mode mainly uses `-buffer` as the active read reservation |
| `-timeout` | HTTP response header timeout in seconds; the download body is not capped by this value |
| `-retries` | Retries per dynamic range |
| `-buffer` | Per-read buffer size |
| `-ua` | HTTP User-Agent, defaults to `mudl` |
| `-progress` | Progress display mode: `summary` (default), `details`, or `none` |
| `-check` | Probe URL and exit without downloading |

## Progress Display

The default `-progress summary` mode updates one line in place, which avoids endless console scrolling on Windows terminals.

To show per-thread speed and active byte ranges:

```powershell
.\mudl-go.exe -progress details "URL"
```

To disable progress output:

```powershell
.\mudl-go.exe -progress none "URL"
```

## Resume With A New URL

If the original URL expires, keep the partial file and run the same output path with a new URL:

```powershell
.\mudl-go.exe "https://new-url.example.com/file.zip" -o "existing-file.zip"
```

## Testing

```powershell
go test -v .
```

## License

MIT

---

# mudl-go 中文说明

`mudl-go` 是一个轻量级命令行 HTTP 下载器，重点是动态多线程 Range 调度。

## 功能特性

- **接近 NDM 思路的动态 Range 调度**：线程使用开放尾巴的 HTTP Range 请求，并从正在下载的大段尾部动态拆分剩余任务。
- **尾部偷取**：当没有排队任务时，空闲线程会从剩余量最大的活动区间切走一段继续下载。
- **新 URL 续传**：原链接过期后，可以保留同一个输出文件，用新 URL 继续下载。
- **文件名识别**：优先读取 `Content-Disposition`，否则使用 URL 文件名或 `-o` 指定的名称。
- **可自定义 User-Agent**：默认是 `mudl`，可用 `-ua` 修改。
- **命令行进度模式**：默认是单行刷新，避免 Windows 终端无限滚屏；需要线程明细时可用 `-progress details`。

## 构建

```powershell
go build -buildvcs=false -o mudl-go.exe .
```

## 使用

```powershell
.\mudl-go.exe "https://example.com/big-file.zip"
```

常用参数：

```powershell
.\mudl-go.exe -c 32 -ua pan.baidu.com -min-chunk 8MB -max-chunk 128MB "URL"
```

如果 URL 里包含 `&`，尤其是带签名参数的 URL，一定要用引号包住：

```powershell
.\mudl-go.exe -ua pan.baidu.com "https://example.com/file.zip?sign=abc&t=123&x-oss-expires=456"
```

参数说明：

| 参数 | 说明 |
| --- | --- |
| `-o` | 输出文件路径 |
| `-c` | 下载线程数 |
| `-min-chunk` | 动态 Range 最小大小 |
| `-max-chunk` | 动态 Range 最大大小 |
| `-reserve` | 保留的预定窗口参数；当前动态模式主要使用 `-buffer` 作为实际读取预定大小 |
| `-timeout` | HTTP 响应头超时时间，单位秒；下载正文不会被这个时间限制 |
| `-retries` | 每个动态区间的重试次数 |
| `-buffer` | 每次读取的缓冲区大小 |
| `-ua` | HTTP User-Agent，默认是 `mudl` |
| `-progress` | 进度显示模式：`summary` 默认单行、`details` 线程明细、`none` 不显示 |
| `-check` | 只探测 URL，不下载 |

## 进度显示

默认 `-progress summary` 会在同一行刷新总进度，避免命令行窗口不断滚动。

查看每个线程的速度和活动字节区间：

```powershell
.\mudl-go.exe -progress details "URL"
```

关闭进度输出：

```powershell
.\mudl-go.exe -progress none "URL"
```

## 使用新 URL 续传

如果原链接过期，保留未完成文件，然后用新 URL 指向同一个输出文件继续：

```powershell
.\mudl-go.exe "https://new-url.example.com/file.zip" -o "existing-file.zip"
```

## 测试

```powershell
go test -v .
```

## 许可证

MIT
