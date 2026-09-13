// Package codepage 实现「解压后按字节重命名」的编码修复层。
//
// 背景（docs/04-中文文件名编码专项方案.md）：
//   - Linux 版 7-Zip 忽略 -mcp（源码实证），但会把归档中的原始字节原样写到磁盘；
//   - 因此修复方式为：读取落盘文件名的原始字节 → 判定编码 → 重命名。
//   - Go 的 string 本身就是原始字节序列，可直接用于判定与重命名。
package codepage

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
	"golang.org/x/text/transform"
)

// Candidate 是一种候选编码。
type Candidate struct {
	Name string
	Code string
}

// Candidates 是默认候选顺序（UTF-8 优先）。
var Candidates = []Candidate{
	{"utf-8", "65001"},
	{"gbk", "936"},
	{"big5", "950"},
	{"sjis", "932"},
	{"euc-kr", "949"},
}

// Detection 是一次编码判定的结果。
type Detection struct {
	Codepage   string  `json:"codepage"`
	Confidence float64 `json:"confidence"`
	NeedsFix   bool    `json:"needsFix"`
	Sampled    int     `json:"sampled"`
	Invalid    int     `json:"invalidUtf8"`
}

// Detect 根据一组文件名（原始字节）判定最可能的编码。
func Detect(names []string) Detection {
	det := Detection{Sampled: len(names)}
	if len(names) == 0 {
		det.Codepage = "utf-8"
		det.Confidence = 1
		return det
	}
	invalid := 0
	for _, n := range names {
		if !utf8.ValidString(n) {
			invalid++
		}
	}
	det.Invalid = invalid
	if invalid == 0 {
		det.Codepage = "utf-8"
		det.Confidence = 1
		return det
	}
	best, bestScore := "gbk", -1.0
	for _, c := range Candidates {
		if c.Name == "utf-8" {
			continue
		}
		score := scoreCandidate(names, c.Name)
		if score > bestScore {
			best, bestScore = c.Name, score
		}
	}
	det.Codepage = best
	det.Confidence = bestScore
	det.NeedsFix = bestScore > 0.5
	return det
}

// scoreCandidate 用「解码后是否为合理文本」给候选编码打分（0..1）。
func scoreCandidate(names []string, enc string) float64 {
	good, total := 0, 0
	for _, n := range names {
		if utf8.ValidString(n) {
			continue
		}
		decoded, err := Decode(n, enc)
		if err != nil {
			total++
			continue
		}
		for _, r := range decoded {
			total++
			if isPlausible(r) {
				good++
			}
		}
	}
	if total == 0 {
		return 0
	}
	return float64(good) / float64(total)
}

func isPlausible(r rune) bool {
	if r == utf8.RuneError {
		return false
	}
	if r < 0x20 && r != '\t' {
		return false
	}
	if r >= 0xE000 && r <= 0xF8FF {
		return false
	}
	if unicode.Is(unicode.Han, r) ||
		unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) ||
		unicode.Is(unicode.Hangul, r) ||
		unicode.Is(unicode.Latin, r) ||
		unicode.Is(unicode.Digit, r) ||
		unicode.Is(unicode.Punct, r) ||
		unicode.Is(unicode.Space, r) {
		return true
	}
	return false
}

// DetectDir 扫描目录（含一级子目录）的文件名并做编码判定。
func DetectDir(root string) Detection {
	entries, err := os.ReadDir(root)
	if err != nil {
		return Detection{Codepage: "utf-8", Confidence: 1}
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if sub, err := os.ReadDir(filepath.Join(root, e.Name())); err == nil {
			for _, se := range sub {
				names = append(names, se.Name())
			}
		}
	}
	return Detect(names)
}

// Decode 用指定编码把原始字节解码为 UTF-8 字符串。
func Decode(raw, enc string) (string, error) {
	switch enc {
	case "", "utf-8", "utf8", "65001":
		return raw, nil
	case "gbk", "gb18030", "936":
		out, _, err := transform.String(simplifiedchinese.GBK.NewDecoder(), raw)
		return out, err
	case "big5", "950":
		out, _, err := transform.String(traditionalchinese.Big5.NewDecoder(), raw)
		return out, err
	case "sjis", "shift-jis", "shift_jis", "cp932", "932":
		out, _, err := transform.String(japanese.ShiftJIS.NewDecoder(), raw)
		return out, err
	case "euc-kr", "euckr", "cp949", "949", "kr":
		out, _, err := transform.String(korean.EUCKR.NewDecoder(), raw)
		return out, err
	default:
		return "", fmt.Errorf("unsupported codepage: %s", enc)
	}
}

// Rename 描述一次重命名。
type Rename struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// PlanRename 扫描已解压目录，返回需要修复的重命名计划（不落盘）。
func PlanRename(root, enc string) ([]Rename, error) {
	var plan []Rename
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if path == root {
			return nil
		}
		name := d.Name()
		if utf8.ValidString(name) {
			return nil
		}
		decoded, derr := Decode(name, enc)
		if derr != nil {
			return nil
		}
		decoded = sanitize(decoded)
		if decoded == "" || decoded == name {
			return nil
		}
		plan = append(plan, Rename{From: path, To: filepath.Join(filepath.Dir(path), decoded)})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(plan, func(i, j int) bool { return len(plan[i].From) > len(plan[j].From) })
	return plan, nil
}

// ApplyRename 执行重命名计划（深→浅），返回成功条数。
func ApplyRename(plan []Rename) (int, error) {
	applied := 0
	for _, r := range plan {
		if _, err := os.Lstat(r.From); err != nil {
			continue
		}
		if _, err := os.Lstat(r.To); err == nil {
			continue
		}
		if err := os.Rename(r.From, r.To); err != nil {
			continue
		}
		applied++
	}
	return applied, nil
}

func sanitize(s string) string {
	s = strings.TrimSpace(s)
	replacer := strings.NewReplacer("/", "_", "\\", "_", "\x00", "")
	s = replacer.Replace(s)
	if s == "." || s == ".." {
		return ""
	}
	return s
}
