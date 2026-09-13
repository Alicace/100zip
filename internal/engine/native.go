// 纯 Go 编解码压缩路径：zstd / lz4 / brotli（单文件与 tar 容器）。
//
// 为什么需要它：7-Zip 26.03 只能创建 7z/BZIP2/GZIP/TAR/WIM/XZ/ZIP，
// 无法创建 .zst/.lz4/.br；而这些格式在现代 NAS 场景很常见（见 docs/18 文档评估）。
// 走纯 Go 实现的好处：不需要携带额外二进制、不涉及 GPL、天然支持交叉编译。
package engine

import (
	"archive/tar"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
	"github.com/pierrec/lz4/v4"

	"github.com/100zip/100zip/internal/apperr"
)

// NativeCodec 描述一个纯 Go 编解码格式。
type NativeCodec struct {
	Name    string // zstd | lz4 | brotli
	Ext     string // .zst | .lz4 | .br
	Tarball bool   // 是否先打 tar 再压缩
}

// ParseNativeFormat 解析格式名；不支持的返回 false。
func ParseNativeFormat(format string) (NativeCodec, bool) {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "zst", "zstd":
		return NativeCodec{"zstd", ".zst", false}, true
	case "tar.zst", "tzst", "tar.zstd":
		return NativeCodec{"zstd", ".tar.zst", true}, true
	case "lz4":
		return NativeCodec{"lz4", ".lz4", false}, true
	case "tar.lz4", "tlz4":
		return NativeCodec{"lz4", ".tar.lz4", true}, true
	case "br", "brotli":
		return NativeCodec{"brotli", ".br", false}, true
	case "tar.br", "tbr":
		return NativeCodec{"brotli", ".tar.br", true}, true
	default:
		return NativeCodec{}, false
	}
}

// NativeFormats 返回支持创建的格式名列表（供 capabilities 使用）。
func NativeFormats() []string {
	return []string{"zst", "tar.zst", "lz4", "tar.lz4", "br", "tar.br"}
}

// CompressNative 用纯 Go 编解码器创建压缩包。
func (e *SevenZip) CompressNative(ctx context.Context, req CompressRequest, onProgress func(Progress)) error {
	codec, ok := ParseNativeFormat(req.Format)
	if !ok {
		return apperr.New(apperr.CodeUnsupportedFormat, "不支持的格式", "查看支持的格式列表")
	}
	dest := req.Dest
	if !strings.HasSuffix(strings.ToLower(dest), codec.Ext) {
		dest += codec.Ext
	}

	// 预扫描总字节数，用于进度百分比
	total, err := totalBytes(req.Sources)
	if err != nil {
		return apperr.Wrap(err, "scan sources")
	}

	out, err := os.Create(dest)
	if err != nil {
		return apperr.New(apperr.CodeDestNotWritable, "无法创建输出文件", "检查目标目录权限")
	}
	defer out.Close()

	counter := &countingWriter{w: out}
	var sink io.WriteCloser
	switch codec.Name {
	case "zstd":
		enc, zerr := zstd.NewWriter(counter, zstd.WithEncoderLevel(zstd.SpeedDefault))
		if zerr != nil {
			return apperr.Wrap(zerr, "init zstd")
		}
		sink = enc
	case "lz4":
		sink = lz4.NewWriter(counter)
	case "brotli":
		sink = brotli.NewWriterLevel(counter, 6)
	default:
		return apperr.New(apperr.CodeUnsupportedFormat, "不支持的格式", "")
	}

	report := func() {
		if onProgress == nil || total <= 0 {
			return
		}
		written := counter.n
		pct := float64(written) / float64(total) * 100
		if pct > 99 {
			pct = 99
		}
		onProgress(Progress{Percent: pct, Bytes: written})
	}

	var werr error
	if codec.Tarball {
		werr = writeTar(ctx, sink, req.Sources, report)
	} else {
		werr = writeSingle(ctx, sink, req.Sources)
	}
	if werr != nil {
		_ = sink.Close()
		return werr
	}
	if err := sink.Close(); err != nil {
		return apperr.Wrap(err, "close codec")
	}
	if err := out.Sync(); err != nil {
		return apperr.Wrap(err, "sync output")
	}
	if onProgress != nil {
		onProgress(Progress{Percent: 100, Bytes: counter.n})
	}
	return nil
}

// writeSingle 压缩单个文件（非 tar 模式）。
func writeSingle(ctx context.Context, w io.Writer, sources []string) error {
	if len(sources) != 1 {
		return apperr.New(apperr.CodeUnsupportedFormat, "单文件压缩只支持一个来源", "请改为 tar.zst / tar.lz4 / tar.br，或只选一个文件")
	}
	f, err := os.Open(sources[0])
	if err != nil {
		return apperr.New(apperr.CodePathNotFound, "找不到来源文件", "确认路径是否正确")
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return apperr.Wrap(err, "stat source")
	}
	if st.IsDir() {
		return apperr.New(apperr.CodeUnsupportedFormat, "目录不能直接压缩为单文件格式", "请选择 tar.zst / tar.lz4 / tar.br")
	}
	_, err = io.Copy(w, f)
	return err
}

// writeTar 把多个来源打包成 tar 写入 w。
func writeTar(ctx context.Context, w io.Writer, sources []string, report func()) error {
	tw := tar.NewWriter(w)
	for _, src := range sources {
		if err := ctx.Err(); err != nil {
			return apperr.New(apperr.CodeJobCancelled, "任务已取消", "")
		}
		base := filepath.Base(src)
		st, err := os.Lstat(src)
		if err != nil {
			return apperr.New(apperr.CodePathNotFound, "找不到来源", "确认路径是否正确")
		}
		if !st.IsDir() {
			if err := addFileToTar(tw, src, base); err != nil {
				return err
			}
			if report != nil {
				report()
			}
			continue
		}
		err = filepath.Walk(src, func(path string, info os.FileInfo, werr error) error {
			if werr != nil {
				return werr
			}
			if err := ctx.Err(); err != nil {
				return apperr.New(apperr.CodeJobCancelled, "任务已取消", "")
			}
			rel, rerr := filepath.Rel(filepath.Dir(src), path)
			if rerr != nil {
				return rerr
			}
			rel = filepath.ToSlash(rel)
			if info.IsDir() {
				hdr := &tar.Header{Name: rel + "/", Mode: 0o755, Typeflag: tar.TypeDir, ModTime: info.ModTime()}
				return tw.WriteHeader(hdr)
			}
			if err := addFileToTar(tw, path, rel); err != nil {
				return err
			}
			if report != nil {
				report()
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return tw.Close()
}

func addFileToTar(tw *tar.Writer, path, name string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	hdr := &tar.Header{
		Name:    filepath.ToSlash(name),
		Mode:    0o644,
		Size:    st.Size(),
		ModTime: st.ModTime(),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err = io.Copy(tw, f)
	return err
}

// totalBytes 统计来源的总字节数（用于进度）。
func totalBytes(sources []string) (int64, error) {
	var total int64
	for _, src := range sources {
		st, err := os.Stat(src)
		if err != nil {
			return 0, err
		}
		if !st.IsDir() {
			total += st.Size()
			continue
		}
		err = filepath.Walk(src, func(_ string, info os.FileInfo, werr error) error {
			if werr != nil {
				return nil
			}
			if !info.IsDir() {
				total += info.Size()
			}
			return nil
		})
		if err != nil {
			return 0, err
		}
	}
	return total, nil
}

type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// 保留变量以避免未使用告警（时间戳在 tar 头中使用）。
var _ = time.Now
var _ = fmt.Sprintf
