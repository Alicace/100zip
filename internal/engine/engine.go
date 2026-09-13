// Package engine 封装解压/压缩引擎（v1 为 7-Zip 静态二进制 7zzs）。
//
// 设计约束（docs/19-开发实施总纲.md §13 不变量）：
//   - 只通过本包执行外部进程；参数以数组传递，禁止 shell 拼接；
//   - 密码等敏感参数不写入日志（由 logging 统一脱敏）；
//   - 进度通过回调上报，不阻塞。
package engine

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/100zip/100zip/internal/apperr"
)

// Engine 是引擎抽象；未来接入 bsdtar 或纯 Go 编解码时实现同一接口。
type Engine interface {
	Capabilities(ctx context.Context) (Capabilities, error)
	List(ctx context.Context, req ListRequest) ([]Entry, error)
	Extract(ctx context.Context, req ExtractRequest, onProgress func(Progress)) error
	Compress(ctx context.Context, req CompressRequest, onProgress func(Progress)) error
	Test(ctx context.Context, req TestRequest) error
}

// Capabilities 描述引擎能力（由探测生成，不在代码里写死）。
type Capabilities struct {
	Engine         string   `json:"engine"`
	Version        string   `json:"version"`
	Path           string   `json:"path"`
	CreateFormats  []string `json:"createFormats"`
	ExtractFormats []string `json:"extractFormats"`
	Features       Features `json:"features"`
}

// Features 为布尔能力位。
type Features struct {
	Split          bool `json:"split"`
	Encrypt        bool `json:"encrypt"`
	EncryptNames   bool `json:"encryptNames"`
	Multithread    bool `json:"multithread"`
	HeaderEncrypt  bool `json:"headerEncrypt"`
	ListStructured bool `json:"listStructured"`
}

// Entry 是压缩包内的一个条目。
type Entry struct {
	Path       string `json:"path"`
	IsDir      bool   `json:"isDir"`
	Size       int64  `json:"size"`
	PackedSize int64  `json:"packedSize"`
	Modified   string `json:"mtime,omitempty"`
	Encrypted  bool   `json:"encrypted,omitempty"`
	CRC        string `json:"crc,omitempty"`
}

// Progress 为一次任务的进度快照。
type Progress struct {
	Percent float64 `json:"percent"`
	Bytes   int64   `json:"bytes"`
	Files   int     `json:"files"`
	Line    string  `json:"-"`
}

// ListRequest 列出压缩包内容。
type ListRequest struct {
	Archive  string
	Password string
}

// ExtractRequest 解压请求。
type ExtractRequest struct {
	Archive   string
	Dest      string
	Entries   []string
	Password  string
	Overwrite string // ask|overwrite|skip|rename
}

// CompressRequest 压缩请求。
type CompressRequest struct {
	Sources    []string
	Dest       string
	Format     string // 7z|zip|tar|gzip|bzip2|xz
	Level      int
	Password   string
	SplitSize  string
	Method     string
	Solid      bool
	Dictionary string
}

// TestRequest 完整性测试。
type TestRequest struct {
	Archive  string
	Password string
}

// SevenZip 是 7-Zip 引擎实现。
type SevenZip struct {
	Bin     string
	Timeout time.Duration
}

// NewSevenZip 创建引擎；bin 为空时返回错误（由上层决定回退策略）。
func NewSevenZip(bin string) (*SevenZip, error) {
	if strings.TrimSpace(bin) == "" {
		return nil, apperr.EngineMissing()
	}
	if st, err := os.Stat(bin); err != nil || st.IsDir() {
		return nil, apperr.EngineMissing()
	}
	if err := checkExecutable(bin); err != nil {
		return nil, apperr.EngineMissing()
	}
	return &SevenZip{Bin: bin, Timeout: 6 * time.Hour}, nil
}

// checkExecutable 在类 Unix 上校验可执行权限；Windows 上仅要求文件存在。
func checkExecutable(path string) error {
	if isWindows() {
		return nil
	}
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	if st.Mode()&0o111 == 0 {
		return errors.New("not executable")
	}
	return nil
}

func isWindows() bool { return os.PathSeparator == '\\' }

// ---------------------------------------------------------------- 参数构造（纯函数）

// ListArgs 构造列表命令：7z l -slt <archive>
func ListArgs(archive, password string) []string {
	args := []string{"l", "-slt", "-p" + password}
	return append(args, archive)
}

// ExtractArgs 构造解压命令。
func ExtractArgs(req ExtractRequest) []string {
	args := []string{"x", "-o" + req.Dest, "-y", "-ao" + overwriteFlag(req.Overwrite)}
	args = append(args, "-mmt=on")
	if req.Password != "" {
		args = append(args, "-p"+req.Password)
	}
	args = append(args, req.Archive)
	args = append(args, req.Entries...)
	return args
}

func overwriteFlag(policy string) string {
	switch policy {
	case "overwrite":
		return "a"
	case "skip":
		return "s"
	case "rename", "ask":
		return "u"
	default:
		return "u"
	}
}

// CompressArgs 构造压缩命令。
func CompressArgs(req CompressRequest) ([]string, error) {
	t, ext, err := formatType(req.Format)
	if err != nil {
		return nil, err
	}
	level := req.Level
	if level < 0 || level > 9 {
		level = 5
	}
	args := []string{"a", "-t" + t, "-mx=" + strconv.Itoa(level)}
	if req.Format == "7z" || req.Format == "zip" {
		args = append(args, "-mmt=on")
	}
	if req.Format == "7z" {
		if req.Solid {
			args = append(args, "-ms=on")
		} else {
			args = append(args, "-ms=off")
		}
		if req.Method != "" {
			args = append(args, "-m0="+req.Method)
		}
		if req.Dictionary != "" {
			args = append(args, "-md="+req.Dictionary)
		}
	}
	if req.Method != "" && (req.Format == "zip") {
		args = append(args, "-mm="+req.Method)
	}
	if req.Password != "" {
		args = append(args, "-p"+req.Password)
		if req.Format == "7z" {
			args = append(args, "-mhe=on")
		}
	}
	if req.SplitSize != "" {
		args = append(args, "-v"+req.SplitSize)
	}
	dest := req.Dest
	if ext != "" && !strings.HasSuffix(strings.ToLower(dest), ext) {
		dest += ext
	}
	args = append(args, dest)
	args = append(args, req.Sources...)
	return args, nil
}

func formatType(format string) (t, ext string, err error) {
	switch strings.ToLower(format) {
	case "7z":
		return "7z", ".7z", nil
	case "zip":
		return "zip", ".zip", nil
	case "tar":
		return "tar", ".tar", nil
	case "gzip", "gz", "tar.gz", "tgz":
		return "gzip", ".gz", nil
	case "bzip2", "bz2", "tar.bz2", "tbz2":
		return "bzip2", ".bz2", nil
	case "xz", "tar.xz", "txz":
		return "xz", ".xz", nil
	default:
		return "", "", apperr.New(apperr.CodeUnsupportedFormat, "不支持的压缩格式", "查看支持的格式列表")
	}
}

// TestArgs 构造完整性测试命令。
func TestArgs(archive, password string) []string {
	args := []string{"t"}
	if password != "" {
		args = append(args, "-p"+password)
	}
	return append(args, archive)
}

// ---------------------------------------------------------------- 执行与解析

// Run 执行一次引擎调用；onStdout 逐行回调（用于进度解析）。
func (e *SevenZip) Run(ctx context.Context, args []string, onStdout func(string)) (string, error) {
	if e.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, e.Timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, e.Bin, args...)
	setProcessGroup(cmd)
	cmd.Env = sanitizedEnv()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", apperr.Wrap(err, "stdout pipe")
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", apperr.Wrap(err, "stderr pipe")
	}
	if err := cmd.Start(); err != nil {
		return "", apperr.Wrap(err, "start engine")
	}
	var wg sync.WaitGroup
	var errBuf strings.Builder
	wg.Add(2)
	go func() {
		defer wg.Done()
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for sc.Scan() {
			if onStdout != nil {
				onStdout(sc.Text())
			}
		}
	}()
	go func() {
		defer wg.Done()
		sc := bufio.NewScanner(stderr)
		sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for sc.Scan() {
			if errBuf.Len() < 64*1024 {
				errBuf.WriteString(sc.Text())
				errBuf.WriteString("\n")
			}
		}
	}()
	wg.Wait()
	err = cmd.Wait()
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return errBuf.String(), apperr.New(apperr.CodeEngineFailed, "任务超时", "查看诊断信息")
		}
		if errors.Is(ctx.Err(), context.Canceled) {
			return errBuf.String(), apperr.New(apperr.CodeJobCancelled, "任务已取消", "")
		}
		return errBuf.String(), MapError(errBuf.String(), err)
	}
	return errBuf.String(), nil
}

// sanitizedEnv 只保留必要环境变量，避免 LD_PRELOAD 之类的劫持。
func sanitizedEnv() []string {
	keep := []string{"PATH", "HOME", "TMPDIR", "TEMP", "TMP", "LANG", "LC_ALL", "SYSTEMROOT", "WINDIR"}
	env := make([]string, 0, len(keep))
	for _, k := range keep {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	return env
}

var passwordWrongRe = regexp.MustCompile(`(?i)(wrong password|password is incorrect|cannot open encrypted archive)`)
var missingVolumeRe = regexp.MustCompile(`(?i)(missing volume|cannot find volume|next volume)`)

// 说明（2026-09-12 实测，7-Zip 26.03，分离流验证：错误文本走 stderr，stdout 只有进度）：
// 分卷缺卷时 stderr 为 "Unexpected end of archive" 或 "Cannot open the file as [7z] archive"，
// 与真实损坏不可从文本区分；此处先归类为「损坏」，分卷上下文的重写见 httpapi.refineVolumeError。
var corruptRe = regexp.MustCompile(`(?i)(unexpected end of archive|crc failed|data error|is not archive|can ?not open the file as)`)
var unsupportedRe = regexp.MustCompile(`(?i)(unsupported method|cannot open as archive)`)
var diskFullRe = regexp.MustCompile(`(?i)(no space left|disk full)`)
var permRe = regexp.MustCompile(`(?i)(permission denied|access is denied)`)

// MapError 把引擎 stderr 映射为应用错误码。
func MapError(stderr string, cause error) *apperr.Error {
	switch {
	case passwordWrongRe.MatchString(stderr):
		return apperr.New(apperr.CodePasswordWrong, "密码错误", "重新输入或从密码库选择")
	case missingVolumeRe.MatchString(stderr):
		return apperr.New(apperr.CodeMissingVolume, "缺少分卷文件", "请确认所有分卷（.002/.003 或 .z02/.r00）与首卷在同一目录")
	case corruptRe.MatchString(stderr):
		return apperr.New(apperr.CodeArchiveCorrupt, "压缩包已损坏或不完整", "尝试完整性测试或重新下载")
	case unsupportedRe.MatchString(stderr):
		return apperr.New(apperr.CodeUnsupportedFormat, "不支持的压缩包格式", "查看支持列表")
	case diskFullRe.MatchString(stderr):
		return apperr.New(apperr.CodeDiskFull, "目标磁盘空间不足", "清理空间或更换位置")
	case permRe.MatchString(stderr):
		return apperr.New(apperr.CodeACLDenied, "当前用户无权访问该位置", "检查文件权限或更换位置")
	}
	// exit 255 = 7-Zip 交互式密码询问失败（真机实测：加密包没带密码时，
	// 7zzs 在 stdout 输出 "Enter password (will not be echoed):"，
	// stdin 为空后在 stderr 写 "Break signaled" 并以 255 退出）。
	// FnSmartZIP 在生产环境同样把 255 与「需要密码」场景绑定处理。
	// stderr 为空、含密码提示或含 "Break signaled" 时归类为「需要密码」，
	// 避免误报为「引擎执行失败」。
	var exitErr *exec.ExitError
	if errors.As(cause, &exitErr) && exitErr.ExitCode() == 255 &&
		(func() bool {
			l := strings.ToLower(stderr)
			return strings.Contains(l, "password") || strings.Contains(l, "break") || strings.TrimSpace(stderr) == ""
		})() {
		return apperr.New(apperr.CodePasswordRequired, "该压缩包需要密码", "在密码框输入密码，或从密码库选择后重试")
	}
	msg := "引擎执行失败"
	if cause != nil {
		msg = fmt.Sprintf("引擎执行失败（%v）", cause)
	}
	return &apperr.Error{Code: apperr.CodeEngineFailed, Message: msg, Hint: "查看诊断信息", Detail: stderr}
}

// ---------------------------------------------------------------- 列表解析

// ParseList 解析 `7z l -slt` 的技术输出。
func ParseList(out string) []Entry {
	var entries []Entry
	var cur Entry
	var folderFlag bool
	inBody := false
	flush := func() {
		if inBody && cur.Path != "" {
			cur.IsDir = folderFlag || strings.HasSuffix(cur.Path, "/") || strings.HasSuffix(cur.Path, `\`)
			entries = append(entries, cur)
		}
		cur = Entry{}
		folderFlag = false
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		// `7z l -slt` 先用一个块描述压缩包自身，随后是分隔线，之后才是条目
		if strings.HasPrefix(strings.TrimSpace(line), "----------") {
			cur = Entry{}
			folderFlag = false
			inBody = true
			continue
		}
		if !inBody {
			continue
		}
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		i := strings.Index(line, " = ")
		if i < 0 {
			continue
		}
		k := strings.TrimSpace(line[:i])
		v := strings.TrimSpace(line[i+3:])
		switch k {
		case "Path":
			if cur.Path != "" {
				flush()
			}
			cur.Path = v
		case "Size":
			cur.Size, _ = strconv.ParseInt(v, 10, 64)
		case "Packed Size":
			cur.PackedSize, _ = strconv.ParseInt(v, 10, 64)
		case "Modified":
			cur.Modified = v
		case "CRC":
			cur.CRC = v
		case "Encrypted":
			cur.Encrypted = strings.EqualFold(v, "+") || strings.EqualFold(v, "true")
		case "Folder", "Attributes":
			if strings.EqualFold(v, "+") || strings.HasPrefix(strings.ToUpper(v), "D") {
				folderFlag = true
			}
		}
	}
	flush()
	// 去掉 7-Zip 输出中的表头伪条目
	filtered := entries[:0]
	for _, e := range entries {
		if e.Path == "" || strings.EqualFold(e.Path, "Path") {
			continue
		}
		filtered = append(filtered, e)
	}
	return filtered
}

// ParseProgress 从引擎输出行中提取百分比与已处理字节数（尽力而为）。
var progressRe = regexp.MustCompile(`(\d{1,3})%`)

// ParseProgress 解析一行进度输出。
func ParseProgress(line string) (Progress, bool) {
	m := progressRe.FindStringSubmatch(line)
	if m == nil {
		return Progress{}, false
	}
	p, _ := strconv.ParseFloat(m[1], 64)
	return Progress{Percent: p, Line: line}, true
}

// ---------------------------------------------------------------- 引擎方法

// Capabilities 探测引擎版本与能力。
func (e *SevenZip) Capabilities(ctx context.Context) (Capabilities, error) {
	out, err := e.Run(ctx, []string{"i"}, nil)
	if err != nil {
		// 部分构建的 `i` 输出走 stdout；失败不阻断，返回静态默认能力
		return e.defaultCapabilities(), nil
	}
	caps := e.defaultCapabilities()
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "7-Zip") && caps.Version == "" {
			fields := strings.Fields(line)
			for _, f := range fields {
				if strings.Count(f, ".") >= 2 {
					caps.Version = f
					break
				}
			}
		}
	}
	return caps, nil
}

func (e *SevenZip) defaultCapabilities() Capabilities {
	return Capabilities{
		Engine:  "7zz",
		Path:    e.Bin,
		Version: "",
		CreateFormats: []string{
			"7z", "zip", "tar", "tar.gz", "tar.bz2", "tar.xz", "gz", "bz2", "xz",
			"zst", "tar.zst", "lz4", "tar.lz4", "br", "tar.br",
		},
		ExtractFormats: []string{
			"7z", "zip", "rar", "tar", "gz", "bz2", "xz", "zst", "iso", "cab", "arj", "lzh",
			"wim", "rpm", "dmg", "vhdx", "vhd", "vmdk", "squashfs", "xar", "cpio", "msi", "nsis",
		},
		Features: Features{
			Split:          true,
			Encrypt:        true,
			EncryptNames:   true,
			Multithread:    true,
			HeaderEncrypt:  true,
			ListStructured: true,
		},
	}
}

// List 列出压缩包条目。
func (e *SevenZip) List(ctx context.Context, req ListRequest) ([]Entry, error) {
	var sb strings.Builder
	_, err := e.Run(ctx, ListArgs(req.Archive, req.Password), func(line string) {
		sb.WriteString(line)
		sb.WriteString("\n")
	})
	if err != nil {
		return nil, err
	}
	return ParseList(sb.String()), nil
}

// Extract 解压（含可选的条目筛选）。
func (e *SevenZip) Extract(ctx context.Context, req ExtractRequest, onProgress func(Progress)) error {
	_, err := e.Run(ctx, ExtractArgs(req), func(line string) {
		if onProgress == nil {
			return
		}
		if p, ok := ParseProgress(line); ok {
			onProgress(p)
		}
	})
	return err
}

// Compress 创建压缩包。
func (e *SevenZip) Compress(ctx context.Context, req CompressRequest, onProgress func(Progress)) error {
	args, err := CompressArgs(req)
	if err != nil {
		return err
	}
	_, err = e.Run(ctx, args, func(line string) {
		if onProgress == nil {
			return
		}
		if p, ok := ParseProgress(line); ok {
			onProgress(p)
		}
	})
	return err
}

// Test 完整性测试。
func (e *SevenZip) Test(ctx context.Context, req TestRequest) error {
	_, err := e.Run(ctx, TestArgs(req.Archive, req.Password), nil)
	return err
}

// ResolveEngine 按优先级解析可用引擎：显式配置 → 系统自带 → 应用自带。
func ResolveEngine(explicit string, appDest string) (string, error) {
	candidates := []string{}
	if explicit != "" {
		candidates = append(candidates, explicit)
	}
	if appDest != "" {
		candidates = append(candidates, filepath.Join(appDest, "vendor", "7zip", "linux-x64", "7zzs"))
		candidates = append(candidates, filepath.Join(appDest, "vendor", "7zip", "linux-x64", "7zz"))
	}
	candidates = append(candidates, "/usr/trim/bin/7zz", "/usr/bin/7z", "/usr/bin/7zz")
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c, nil
		}
	}
	return "", apperr.EngineMissing()
}

// CopyStream 用于把引擎输出直接转发的辅助函数（保留接口，便于未来流式）。
func CopyStream(dst io.Writer, src io.Reader) error {
	_, err := io.Copy(dst, src)
	return err
}
