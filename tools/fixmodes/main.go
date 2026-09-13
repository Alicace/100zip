// fixmodes 修正 FPK 内部的 Unix 权限位。
//
// 背景：fnpack 在 Windows 上打包时无法保留可执行位，产出的 FPK 里
// bin/100zip、vendor/**/7zzs、cmd/* 都是 0666，装到 Linux（飞牛）上无法执行。
// 本工具在打包后原地修正：
//   - 目录 → 0755
//   - 可执行文件（应用二进制、引擎二进制、生命周期脚本）→ 0755
//   - 其它文件 → 0644
//
// 用法：fixmodes -in dist/100zip.fpk -out dist/100zip-fixed.fpk
package main

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path"
	"strings"
)

func main() {
	in := flag.String("in", "", "输入 .fpk")
	out := flag.String("out", "", "输出 .fpk")
	flag.Parse()
	if *in == "" || *out == "" {
		log.Fatal("用法: fixmodes -in <in.fpk> -out <out.fpk>")
	}
	if err := run(*in, *out); err != nil {
		log.Fatalf("fixmodes: %v", err)
	}
}

func run(in, out string) error {
	f, err := os.Open(in)
	if err != nil {
		return err
	}
	defer f.Close()

	br := bufio.NewReader(f)
	magic, err := br.Peek(2)
	if err != nil {
		return err
	}
	isGzip := magic[0] == 0x1F && magic[1] == 0x8B

	var src io.Reader = br
	if isGzip {
		gzr, err := gzip.NewReader(br)
		if err != nil {
			return err
		}
		defer gzr.Close()
		src = gzr
	}

	of, err := os.Create(out)
	if err != nil {
		return err
	}
	defer of.Close()

	var dst io.Writer = of
	var gzw *gzip.Writer
	if isGzip {
		gzw = gzip.NewWriter(of)
		dst = gzw
	}

	tr := tar.NewReader(src)
	tw := tar.NewWriter(dst)

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		nh := *hdr
		if hdr.Name == "app.tgz" {
			fixed, err := fixInner(tr)
			if err != nil {
				return fmt.Errorf("处理 app.tgz: %w", err)
			}
			nh.Size = int64(len(fixed))
			nh.Mode = 0o644
			if err := tw.WriteHeader(&nh); err != nil {
				return err
			}
			if _, err := tw.Write(fixed); err != nil {
				return err
			}
			continue
		}
		nh.Mode = outerMode(hdr)
		if err := tw.WriteHeader(&nh); err != nil {
			return err
		}
		if hdr.Typeflag == tar.TypeReg && hdr.Size > 0 {
			if _, err := io.Copy(tw, tr); err != nil {
				return err
			}
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if gzw != nil {
		if err := gzw.Close(); err != nil {
			return err
		}
	}
	return nil
}

// fixInner 解压 app.tgz → 修正内部权限 → 重新 gzip。
func fixInner(r io.Reader) ([]byte, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, err
	}
	itr := tar.NewReader(gz)

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	for {
		hdr, err := itr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		nh := *hdr
		nh.Mode = innerMode(hdr)
		if err := tw.WriteHeader(&nh); err != nil {
			return nil, err
		}
		if hdr.Typeflag == tar.TypeReg && hdr.Size > 0 {
			if _, err := io.Copy(tw, itr); err != nil {
				return nil, err
			}
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func outerMode(h *tar.Header) int64 {
	if h.Typeflag == tar.TypeDir {
		return 0o755
	}
	if strings.HasPrefix(h.Name, "cmd/") && h.Typeflag == tar.TypeReg {
		return 0o755 // 生命周期脚本
	}
	return 0o644
}

func innerMode(h *tar.Header) int64 {
	if h.Typeflag == tar.TypeDir {
		return 0o755
	}
	name := path.Clean(h.Name)
	base := path.Base(name)
	switch {
	case strings.HasPrefix(name, "bin/"):
		return 0o755
	case strings.Contains(name, "vendor/") && (base == "7zzs" || base == "7zz" || base == "bsdtar"):
		return 0o755
	case strings.HasSuffix(name, ".sh"):
		return 0o755
	case base == "index.cgi" || base == "api.cgi":
		return 0o755
	}
	return 0o644
}
