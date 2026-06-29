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

`mudl-go` 是一个专注命令行的动态多线程 HTTP 下载器。

核心目标是避免传统固定分片下载器的问题：某些线程提前下载完后空闲，而慢线程还在拖尾。`mudl-go` 会持续分配 Range 任务，并在必要时从正在下载的大区间尾部切走一段给空闲线程继续下载。

快速使用：

```powershell
.\mudl-go.exe "URL" -c 32 -min-chunk 8MB -max-chunk 128MB -reserve 64MB
```

如果链接过期，保留未完成文件和 `.mudl.json`，换新 URL 后继续：

```powershell
.\mudl-go.exe "新URL" -o "原文件名.zip"
```
