package paths

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// VolumeInfo 描述一个分卷包的序列信息。
type VolumeInfo struct {
	IsVolume bool     `json:"isVolume"`
	First    string   `json:"first,omitempty"`
	Missing  []string `json:"missing,omitempty"`
	Total    int      `json:"total,omitempty"`
}

// dirFiles 是一次 os.ReadDir 得到的「同目录文件名集合」。
// 用内存集合替代逐卷 os.Stat：单次扫描 1 万个候选编号时，
// 系统调用次数从 1 万次降到 1 次读目录，批量扫描大目录时差距尤其明显。
// 键统一小写、值保留真实大小写：WinRAR 可能生成 .Z01/.RAR 大写卷名，
// Linux 文件系统区分大小写，首卷名必须回填真实大小写，否则批量页会误跳过首卷。
type dirFiles map[string]string

func readDirFiles(dir string) dirFiles {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	m := make(dirFiles, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m[strings.ToLower(e.Name())] = e.Name()
	}
	return m
}

func (m dirFiles) has(name string) bool {
	if m == nil {
		return false
	}
	_, ok := m[strings.ToLower(name)]
	return ok
}

// actual 返回 name 在目录中的真实大小写文件名；不存在返回空串。
func (m dirFiles) actual(name string) string {
	if m == nil {
		return ""
	}
	return m[strings.ToLower(name)]
}

var (
	numericVolRe = regexp.MustCompile(`(?i)^(.*)\.(\d{3})$`)
	zVolRe       = regexp.MustCompile(`(?i)^(.*)\.z(\d{2})$`)
	partVolRe    = regexp.MustCompile(`(?i)^(.*)\.part(\d+)\.(rar|7z)$`)
	volHeadRe    = regexp.MustCompile(`(?i)^(.*)\.(zip|7z|rar)$`)
)

// AnalyzeVolumes 分析一个文件是否属于分卷包，并检查同目录下是否有缺口。
//
// 支持：name.001/.002、name.z01/.z02、name.part1.rar/.part2.rar、name.rar + name.r00
// 返回 Missing 为「缺失的卷文件名」（只检测缺口，不断言末卷）。
func AnalyzeVolumes(path string) VolumeInfo {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	files := readDirFiles(dir)
	if files == nil {
		// 目录不可读时无法判断序列，保持「非分卷」，避免把当前文件自身误报为缺失卷
		return VolumeInfo{}
	}

	// 通用数字分卷 .001/.002
	if m := numericVolRe.FindStringSubmatch(base); m != nil {
		prefix := m[1]
		return scanSequence(files, prefix, ".%03d", 1, startIndexFromName(m[2]))
	}
	// WinRAR 标准 .z01/.z02
	if m := zVolRe.FindStringSubmatch(base); m != nil {
		prefix := m[1]
		return scanSequence(files, prefix, ".z%02d", 1, atoi(m[2]))
	}
	// .part1.rar / .part1.7z
	if m := partVolRe.FindStringSubmatch(base); m != nil {
		prefix := m[1] + ".part"
		ext := "." + m[3]
		return scanSequence(files, prefix, "%d"+ext, 1, atoi(m[2]))
	}
	// 旧式 .rar + .r00/.r01
	if strings.HasSuffix(strings.ToLower(base), ".rar") {
		stem := strings.TrimSuffix(base, filepath.Ext(base))
		if v := scanRarClassic(files, stem); v.IsVolume {
			return v
		}
	}
	// WinRAR 标准分卷：data.zip + data.z01/data.z02（首卷是 .zip 自身）
	if m := volHeadRe.FindStringSubmatch(base); m != nil {
		stem := m[1]
		if files.has(stem + ".z01") {
			info := scanSequence(files, stem, ".z%02d", 1, 1)
			info.IsVolume = true
			info.First = base // 7-Zip 可直接打开 .zip；.z01 是内部第 1 部分
			info.Total = countZVolumes(files, stem) + 1
			return info
		}
	}
	return VolumeInfo{}
}

// countZVolumes 统计已存在的 .zNN 卷数量（不含 .zip 自身）。
func countZVolumes(files dirFiles, stem string) int {
	n := 0
	for i := 1; i <= 999; i++ {
		name := fmt.Sprintf("%s.z%02d", stem, i)
		if files.has(name) {
			n++
		}
	}
	return n
}

func startIndexFromName(s string) int {
	n := atoi(s)
	if n < 1 {
		n = 1
	}
	return n
}

func atoi(s string) int {
	v, err := strconv.Atoi(strings.TrimLeft(s, "0"))
	if err != nil {
		return 1
	}
	return v
}

// scanSequence 从 start 开始逐卷检查；遇到已存在的最大编号后停止，返回中间缺口。
// files 为同目录文件名集合（见 readDirFiles），全程无系统调用。
func scanSequence(files dirFiles, prefix, format string, start, current int) VolumeInfo {
	info := VolumeInfo{IsVolume: true}
	maxSeen := 0
	for i := start; i <= 9999; i++ {
		name := prefix + fmt.Sprintf(format, i)
		if files.has(name) {
			maxSeen = i
		}
	}
	if current > maxSeen {
		maxSeen = current
	}
	info.Total = maxSeen
	for i := start; i <= maxSeen; i++ {
		name := prefix + fmt.Sprintf(format, i)
		if !files.has(name) {
			info.Missing = append(info.Missing, name)
		}
	}
	if maxSeen >= start {
		first := prefix + fmt.Sprintf(format, start)
		if real := files.actual(first); real != "" {
			first = real
		}
		info.First = first
	}
	return info
}

// scanRarClassic 处理 name.rar + name.r00 / name.r01（存在 .r00 才算分卷）。
func scanRarClassic(files dirFiles, stem string) VolumeInfo {
	info := VolumeInfo{}
	hasR00 := false
	for i := 0; i <= 999; i++ {
		name := fmt.Sprintf("%s.r%02d", stem, i)
		if files.has(name) {
			hasR00 = true
			break
		}
	}
	if !hasR00 {
		return info
	}
	info.IsVolume = true
	info.First = stem + ".rar"
	if real := files.actual(info.First); real != "" {
		info.First = real
	}
	maxSeen := -1
	for i := 0; i <= 999; i++ {
		name := fmt.Sprintf("%s.r%02d", stem, i)
		if files.has(name) {
			maxSeen = i
		}
	}
	for i := 0; i <= maxSeen; i++ {
		name := fmt.Sprintf("%s.r%02d", stem, i)
		if !files.has(name) {
			info.Missing = append(info.Missing, name)
		}
	}
	info.Total = maxSeen + 2 // .rar 自身算一卷
	return info
}

// Z01ForZipHead 若 path 是 data.zip/data.7z/data.rar 且同目录存在 data.z01，
// 返回 data.z01 的完整路径；否则返回空串。
//
// 用途：WinRAR 风格分卷（data.zip + data.z01…）在 7-Zip 里若直接打开 .zip 失败，
// 可改用 .z01 作为入口重试——ZipIn.cpp 在打开 .z01 时会主动去找同名的 .zip。
func Z01ForZipHead(path string) string {
	m := volHeadRe.FindStringSubmatch(filepath.Base(path))
	if m == nil {
		return ""
	}
	dir := filepath.Dir(path)
	name := readDirFiles(dir).actual(m[1] + ".z01")
	if name != "" {
		return filepath.Join(dir, name)
	}
	return ""
}

// FirstVolumeOf 返回分卷首卷路径（若不是分卷则原样返回）。
func FirstVolumeOf(path string) string {
	v := AnalyzeVolumes(path)
	if !v.IsVolume || v.First == "" {
		return path
	}
	return filepath.Join(filepath.Dir(path), v.First)
}

// IsVolumeMember 判断文件是否是某个分卷包的「非首卷成员」：
//   - 后续卷（.002/.010、.z02/.z10、.r00/.r01、.part2+）一律是；
//   - .z01 段需要同目录存在同名 .zip/.7z/.rar 才算（WinRAR 风格分卷）。
//
// 用途：批量扫描时把分卷包的其余成员从列表中隐去，只保留首卷，避免误勾选。
func IsVolumeMember(p string) bool {
	if IsVolumeTail(p) {
		return true
	}
	lower := strings.ToLower(filepath.Base(p))
	if !strings.HasSuffix(lower, ".z01") {
		return false
	}
	stem := strings.TrimSuffix(lower, ".z01")
	files := readDirFiles(filepath.Dir(p))
	for _, ext := range []string{".zip", ".7z", ".rar"} {
		if files.has(stem + ext) {
			return true
		}
	}
	return false
}
