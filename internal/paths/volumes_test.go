package paths

import (
	"os"
	"path/filepath"
	"testing"
)

func touch(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAnalyzeVolumesGap(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "movie.7z.001")
	touch(t, dir, "movie.7z.003")
	v := AnalyzeVolumes(filepath.Join(dir, "movie.7z.001"))
	if !v.IsVolume {
		t.Fatal("应识别为分卷包")
	}
	if v.First != "movie.7z.001" {
		t.Errorf("首卷识别错误：%s", v.First)
	}
	if len(v.Missing) != 1 || v.Missing[0] != "movie.7z.002" {
		t.Errorf("缺卷检测错误：%+v", v.Missing)
	}
	if v.Total != 3 {
		t.Errorf("总卷数应为 3，得到 %d", v.Total)
	}
}

func TestAnalyzeVolumesComplete(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "a.zip.001")
	touch(t, dir, "a.zip.002")
	v := AnalyzeVolumes(filepath.Join(dir, "a.zip.001"))
	if len(v.Missing) != 0 {
		t.Errorf("完整分卷不应报缺口：%+v", v.Missing)
	}
	if v.Total != 2 {
		t.Errorf("总卷数应为 2，得到 %d", v.Total)
	}
}

func TestAnalyzeVolumesZAndPart(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "data.zip")
	touch(t, dir, "data.z01")
	touch(t, dir, "data.z02")
	v := AnalyzeVolumes(filepath.Join(dir, "data.zip"))
	if !v.IsVolume || len(v.Missing) != 0 {
		t.Errorf("z01/z02 场景识别异常：%+v", v)
	}

	dir2 := t.TempDir()
	touch(t, dir2, "big.part1.rar")
	// 只有 part1 时无法推断总卷数（是否还有 part2 取决于包本身），
	// 因此这里只要求识别为分卷；「缺卷」由引擎在解压时报错并被映射为 MISSING_VOLUME。
	v2 := AnalyzeVolumes(filepath.Join(dir2, "big.part1.rar"))
	if !v2.IsVolume || v2.First != "big.part1.rar" {
		t.Errorf("part1 场景识别异常：%+v", v2)
	}

	// 中间缺口必须能检出：part1 + part3（缺 part2）
	dir3 := t.TempDir()
	touch(t, dir3, "x.part1.rar")
	touch(t, dir3, "x.part3.rar")
	v3 := AnalyzeVolumes(filepath.Join(dir3, "x.part1.rar"))
	if len(v3.Missing) != 1 || v3.Missing[0] != "x.part2.rar" {
		t.Errorf("part 中间缺口检测异常：%+v", v3)
	}
}

func TestAnalyzeVolumesNonVolume(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "plain.zip")
	v := AnalyzeVolumes(filepath.Join(dir, "plain.zip"))
	if v.IsVolume {
		t.Errorf("普通 zip 不应识别为分卷：%+v", v)
	}
}

func TestAnalyzeVolumesUppercase(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "Movie.ZIP")
	touch(t, dir, "Movie.Z01")
	touch(t, dir, "Movie.Z02")

	v := AnalyzeVolumes(filepath.Join(dir, "Movie.ZIP"))
	if !v.IsVolume || v.First != "Movie.ZIP" || len(v.Missing) != 0 {
		t.Errorf("大写 zip 头识别异常：%+v", v)
	}
	// .z01 段也要能定位回真实大小写的首卷（否则扫描页会误跳过该文件）
	v2 := AnalyzeVolumes(filepath.Join(dir, "Movie.Z01"))
	if !v2.IsVolume || v2.First != "Movie.Z01" {
		t.Errorf("大写 z01 首卷回填异常：%+v", v2)
	}
}

func TestIsVolumeMember(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "Movie.ZIP")
	touch(t, dir, "Movie.Z01")
	touch(t, dir, "movie.7z.002")

	if !IsVolumeMember(filepath.Join(dir, "Movie.Z01")) {
		t.Error("WinRAR 风格 .Z01 应视为分卷成员")
	}
	if !IsVolumeMember(filepath.Join(dir, "movie.7z.002")) {
		t.Error(".002 后续卷应视为分卷成员")
	}
	if IsVolumeMember(filepath.Join(dir, "Movie.ZIP")) {
		t.Error("zip 头是首卷，不是成员")
	}
}
