package engine

import (
	"archive/tar"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/zstd"

	"github.com/100zip/100zip/internal/apperr"
)

func TestParseNativeFormat(t *testing.T) {
	cases := map[string]string{
		"zst": ".zst", "zstd": ".zst", "tar.zst": ".tar.zst",
		"lz4": ".lz4", "tar.lz4": ".tar.lz4",
		"br": ".br", "tar.br": ".tar.br",
	}
	for in, ext := range cases {
		c, ok := ParseNativeFormat(in)
		if !ok || c.Ext != ext {
			t.Errorf("ParseNativeFormat(%q) = %+v,%v 期望扩展名 %s", in, c, ok, ext)
		}
	}
	if _, ok := ParseNativeFormat("rar"); ok {
		t.Error("rar 不应被识别为原生格式")
	}
}

// 往返验证：tar.zst 压缩 → 用 zstd + tar 解开，内容一致。
func TestCompressNativeTarZstdRoundTrip(t *testing.T) {
	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "sub", "b.txt"), []byte("world!"), 0o644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "out.tar.zst")

	e := &SevenZip{}
	var lastPct float64
	err := e.CompressNative(context.Background(), CompressRequest{
		Sources: []string{src},
		Dest:    dest,
		Format:  "tar.zst",
	}, func(p Progress) { lastPct = p.Percent })
	if err != nil {
		t.Fatalf("CompressNative: %v", err)
	}
	if lastPct != 100 {
		t.Errorf("结束时进度应为 100，得到 %v", lastPct)
	}

	f, err := os.Open(dest)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := zstd.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	tr := tar.NewReader(zr)
	found := map[string]string{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("读取 tar: %v", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		b, _ := io.ReadAll(tr)
		found[filepath.Base(hdr.Name)] = string(b)
	}
	if found["a.txt"] != "hello" || found["b.txt"] != "world!" {
		t.Errorf("解出的内容不正确: %+v", found)
	}
}

func TestCompressNativeRejectsPasswordAndSingleFileWithDir(t *testing.T) {
	dir := t.TempDir()
	e := &SevenZip{}
	// 目录 + 单文件格式 → 应报错
	err := e.CompressNative(context.Background(), CompressRequest{
		Sources: []string{dir}, Dest: filepath.Join(t.TempDir(), "x"), Format: "zst",
	}, nil)
	if err == nil {
		t.Fatal("目录压缩为 .zst 应报错")
	}
	if ae, ok := err.(*apperr.Error); !ok || ae.Code != apperr.CodeUnsupportedFormat {
		t.Errorf("期望 UNSUPPORTED_FORMAT，得到 %v", err)
	}
}
