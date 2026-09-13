package codepage

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// "测试文件.txt" 的 GBK 字节
var gbkName = string([]byte{0xB2, 0xE2, 0xCA, 0xD4, 0xCE, 0xC4, 0xBC, 0xFE, '.', 't', 'x', 't'})

func TestDecodeGBK(t *testing.T) {
	got, err := Decode(gbkName, "gbk")
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got != "测试文件.txt" {
		t.Errorf("GBK 解码结果 = %q，期望 %q", got, "测试文件.txt")
	}
}

func TestDetectGBKVerusUTF8(t *testing.T) {
	det := Detect([]string{"normal.txt", "readme.md"})
	if det.NeedsFix || det.Codepage != "utf-8" {
		t.Errorf("纯 ASCII 不应需要修复: %+v", det)
	}

	det2 := Detect([]string{"docs", gbkName})
	if !det2.NeedsFix {
		t.Errorf("含非法 UTF-8 字节应判定需要修复: %+v", det2)
	}
	if det2.Codepage != "gbk" {
		t.Errorf("候选编码 = %s，期望 gbk: %+v", det2.Codepage, det2)
	}
}

func TestPlanAndApplyRename(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 文件名使用 UTF-16，无法表示非法 UTF-8 原始字节；该用例在 Linux 上有意义")
	}
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	// 用原始 GBK 字节作为文件名落盘（模拟 7-Zip 在 Linux 上的行为）
	old := filepath.Join(sub, gbkName)
	if err := os.WriteFile(old, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	det := DetectDir(dir)
	if !det.NeedsFix {
		t.Fatalf("应检测到需要修复: %+v", det)
	}
	plan, err := PlanRename(dir, det.Codepage)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 1 {
		t.Fatalf("期望 1 条重命名，得到 %d: %+v", len(plan), plan)
	}
	n, err := ApplyRename(plan)
	if err != nil || n != 1 {
		t.Fatalf("重命名失败 n=%d err=%v", n, err)
	}
	if _, err := os.Stat(filepath.Join(sub, "测试文件.txt")); err != nil {
		t.Errorf("重命名后应存在正确文件名: %v", err)
	}
}
