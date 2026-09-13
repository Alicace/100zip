package engine

import (
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"github.com/100zip/100zip/internal/apperr"
)

func TestCompressArgsFormats(t *testing.T) {
	cases := []struct {
		format string
		wantT  string
		ext    string
	}{
		{"7z", "-t7z", ".7z"},
		{"zip", "-tzip", ".zip"},
		{"tar", "-ttar", ".tar"},
		{"gz", "-tgzip", ".gz"},
		{"tar.gz", "-tgzip", ".gz"},
		{"bz2", "-tbzip2", ".bz2"},
		{"xz", "-txz", ".xz"},
	}
	for _, c := range cases {
		args, err := CompressArgs(CompressRequest{Format: c.format, Dest: "/out/a", Sources: []string{"/in/x"}})
		if err != nil {
			t.Fatalf("format %s: %v", c.format, err)
		}
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, c.wantT) {
			t.Errorf("format %s: args %v 缺少 %s", c.format, args, c.wantT)
		}
		if !strings.Contains(args[len(args)-2], c.ext) {
			t.Errorf("format %s: 目标未补扩展名: %v", c.format, args[len(args)-2])
		}
	}
	if _, err := CompressArgs(CompressRequest{Format: "rar", Dest: "/out/a"}); err == nil {
		t.Error("rar 创建应被拒绝（合规红线）")
	}
}

func TestExtractArgsOverwriteAndPassword(t *testing.T) {
	a := ExtractArgs(ExtractRequest{Archive: "/a.zip", Dest: "/out", Overwrite: "skip", Password: "pw"})
	joined := strings.Join(a, " ")
	if !strings.Contains(joined, "-aos") {
		t.Errorf("skip 应映射为 -aos: %v", a)
	}
	if !strings.Contains(joined, "-ppw") {
		t.Errorf("应包含密码参数: %v", a)
	}
	b := ExtractArgs(ExtractRequest{Archive: "/a.zip", Dest: "/out", Overwrite: "overwrite"})
	if !strings.Contains(strings.Join(b, " "), "-aoa") {
		t.Errorf("overwrite 应映射为 -aoa: %v", b)
	}
}

func TestParseList(t *testing.T) {
	out := `
Path = /tmp/a.zip
Type = zip
Physical Size = 2048

----------

Path = docs
Folder = +
Size = 0

Path = docs/报告.pdf
Size = 1048576
Packed Size = 987654
Modified = 2026-08-01 10:00:00
CRC = ABCD1234
Encrypted = +

`
	entries := ParseList(out)
	if len(entries) != 2 {
		t.Fatalf("期望 2 个条目，得到 %d: %+v", len(entries), entries)
	}
	if !entries[0].IsDir {
		t.Error("docs 应识别为目录")
	}
	if entries[1].Size != 1048576 || entries[1].PackedSize != 987654 {
		t.Errorf("大小解析错误: %+v", entries[1])
	}
	if !entries[1].Encrypted {
		t.Error("Encrypted=+ 应识别为加密")
	}
}

func TestMapError(t *testing.T) {
	cases := map[string]apperr.Code{
		"ERROR: Wrong password?":         apperr.CodePasswordWrong,
		"ERROR: CRC Failed in file":      apperr.CodeArchiveCorrupt,
		"ERROR: Unsupported Method":      apperr.CodeUnsupportedFormat,
		"ERROR: No space left on device": apperr.CodeDiskFull,
		"ERROR: Permission denied":       apperr.CodeACLDenied,
	}
	for stderr, want := range cases {
		got := MapError(stderr, nil)
		if got.Code != want {
			t.Errorf("stderr=%q 映射为 %s，期望 %s", stderr, got.Code, want)
		}
	}
}

func TestMapErrorExit255(t *testing.T) {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/c", "exit 255")
	} else {
		cmd = exec.Command("sh", "-c", "exit 255")
	}
	err := cmd.Run()
	if err == nil {
		t.Fatal("模拟进程应非零退出")
	}
	// 空 stderr + exit 255：加密包没带密码的实测路径 → 需要密码
	got := MapError("", err)
	if got.Code != apperr.CodePasswordRequired {
		t.Fatalf("exit 255 + 空 stderr 应归类为需要密码，得到 %#v", got)
	}
	// "Break signaled" + exit 255：Linux 真机实测的密码询问失败文本 → 需要密码
	gotBreak := MapError("Break signaled\n", err)
	if gotBreak.Code != apperr.CodePasswordRequired {
		t.Fatalf("exit 255 + Break signaled 应归类为需要密码，得到 %#v", gotBreak)
	}
	// exit 255 + 无关错误文本：不能误判为需要密码，保持引擎失败
	got2 := MapError("some unrelated engine text", err)
	if got2.Code != apperr.CodeEngineFailed {
		t.Fatalf("exit 255 + 无关文本应保持引擎失败，得到 %#v", got2)
	}
}
