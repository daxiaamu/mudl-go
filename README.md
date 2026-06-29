# mudl-go

A lightweight command-line HTTP downloader focused on dynamic multi-threaded range scheduling.

## Features

- **Dynamic range allocation**: workers continuously fetch new byte ranges instead of being locked into fixed partitions.
- **Tail stealing**: when the pending queue is empty, idle workers can split work from the tail of an active large range.
- **Resume metadata**: unfinished ranges are saved in `.mudl.json` state files.
- **Fresh URL resume**: resume an expired download by running the same output path with a new URL.
- **Filename detection**: prefers `Content-Disposition`, then falls back to the URL basename or `-o`.
- **Live CLI status**: shows active thread count, total progress, ETA, and per-thread speed.

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
.\mudl-go.exe "URL" -c 32 -min-chunk 8MB -max-chunk 128MB -reserve 64MB
```

Options:

| Option | Description |
| --- | --- |
| `-o` | Output file path |
| `-c` | Worker count |
| `-min-chunk` | Minimum dynamic range size |
| `-max-chunk` | Maximum dynamic range size |
| `-reserve` | Bytes reserved per HTTP Range request |
| `-timeout` | HTTP timeout in seconds |
| `-retries` | Retries per reserved range |
| `-buffer` | Per-read buffer size |
| `-ua` | HTTP User-Agent, defaults to `mudl` |
| `-check` | Probe URL and exit without downloading |

## Resume With A New URL

If the original URL expires, keep the partial file and `.mudl.json` file, then run:

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

`mudl-go` 是一个专注命令行的轻量 HTTP 下载器，核心是动态多线程 Range 调度。

## 功能特性

- **动态 Range 分配**：线程持续领取新的字节区间，而不是启动时固定分片后一直等到结束。
- **尾部偷取**：当待下载队列为空时，空闲线程可以从仍在下载的大区间尾部切走一段继续下载。
- **断点元数据**：未完成区间会保存到 `.mudl.json` 状态文件。
- **新 URL 续传**：原链接过期后，可以保留原文件和 `.mudl.json`，用新 URL 继续同一个输出文件。
- **文件名识别**：优先读取 `Content-Disposition`，否则使用 URL 文件名或 `-o` 指定的名称。
- **实时命令行状态**：显示当前活跃线程数、总进度、剩余时间和每个线程速度。

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
.\mudl-go.exe "URL" -c 32 -min-chunk 8MB -max-chunk 128MB -reserve 64MB
```

参数说明：

| 参数 | 说明 |
| --- | --- |
| `-o` | 输出文件路径 |
| `-c` | 下载线程数 |
| `-min-chunk` | 动态 Range 最小大小 |
| `-max-chunk` | 动态 Range 最大大小 |
| `-reserve` | 每次 HTTP Range 请求预留的字节数 |
| `-timeout` | HTTP 超时时间，单位秒 |
| `-retries` | 每个预留区间的重试次数 |
| `-buffer` | 每次读取的缓冲区大小 |
| `-ua` | HTTP User-Agent，默认是 `mudl` |
| `-check` | 只探测 URL，不下载 |

## 使用新 URL 续传

如果原链接过期，保留未完成文件和 `.mudl.json`，换新 URL 后继续：

```powershell
.\mudl-go.exe "https://new-url.example.com/file.zip" -o "existing-file.zip"
```

## 测试

```powershell
go test -v .
```

## 许可证

MIT
