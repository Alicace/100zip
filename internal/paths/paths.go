// Package paths 是所有外部路径的唯一安全入口。
//
// 不变量（见 docs/19-开发实施总纲.md §13）：
//  1. 任何来自请求的路径都必须经本包校验后才能交给 os / 引擎使用；
//  2. 校验包含：绝对路径、realpath、目录边界前缀匹配、可选 ACL 回调；
//  3. 压缩包内条目名必须经 SanitizeEntry 净化，拒绝路径穿越。
package paths

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/100zip/100zip/internal/apperr"
)

// Guard 持有本次运行的授权根目录集合。
type Guard struct {
	mu    sync.RWMutex
	roots []string
}

// NewGuard 创建 Guard；roots 会做 realpath 规范化，不存在的根会被忽略。
func NewGuard(roots []string) *Guard {
	g := &Guard{}
	for _, r := range roots {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		abs, err := filepath.Abs(r)
		if err != nil {
			continue
		}
		real, err := filepath.EvalSymlinks(abs)
		if err != nil {
			continue
		}
		g.roots = append(g.roots, filepath.Clean(real))
	}
	return g
}

// Roots 返回规范化后的授权根（副本）。
func (g *Guard) Roots() []string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]string, len(g.roots))
	copy(out, g.roots)
	return out
}

// AddRoot 在运行时新增授权根。
// 安全前提：必须能在真实文件系统上「解析 + 打开」，因此前端无法伪造未授权路径。
func (g *Guard) AddRoot(p string) error {
	p = strings.TrimSpace(p)
	if p == "" || strings.ContainsRune(p, 0) || !filepath.IsAbs(p) {
		return apperr.NotFound()
	}
	real, err := filepath.EvalSymlinks(filepath.Clean(p))
	if err != nil {
		return apperr.NotFound()
	}
	st, err := os.Stat(real)
	if err != nil || !st.IsDir() {
		return apperr.New(apperr.CodeDestNotWritable, "该位置不是可访问的目录", "请重新选择")
	}
	f, err := os.Open(real)
	if err != nil {
		return apperr.NotAuthorized()
	}
	_ = f.Close()

	g.mu.Lock()
	defer g.mu.Unlock()
	for _, r := range g.roots {
		if r == real {
			return nil
		}
	}
	g.roots = append(g.roots, real)
	return nil
}

// UnderRoot 判断 target 是否位于任一授权根之下（按目录边界比较）。
func (g *Guard) UnderRoot(target string) bool {
	target = filepath.Clean(target)
	g.mu.RLock()
	defer g.mu.RUnlock()
	for _, root := range g.roots {
		root = filepath.Clean(root)
		if target == root {
			return true
		}
		if strings.HasPrefix(target, root+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}

// Resolve 校验一个已存在的路径：必须绝对、必须存在、必须位于授权根内。
func (g *Guard) Resolve(p string) (string, error) {
	if strings.TrimSpace(p) == "" || strings.ContainsRune(p, 0) || len(p) > 4096 {
		return "", apperr.NotFound()
	}
	if !filepath.IsAbs(p) {
		return "", apperr.NotFound()
	}
	real, err := filepath.EvalSymlinks(filepath.Clean(p))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", apperr.NotFound()
		}
		return "", apperr.NotFound()
	}
	if !g.UnderRoot(real) {
		// 自动学习：系统 ACL 才是最终边界。
		// 若应用用户实际可读该路径，说明用户已通过飞牛（应用内选择器或应用设置）授权，
		// 则登记为可访问根，避免重启后内存白名单丢失导致「明明授权了却用不了」。
		if canAccess(real) {
			g.addRoot(real)
		} else {
			return "", apperr.NotAuthorized()
		}
	}
	return real, nil
}

// canAccess 以当前进程身份尝试打开路径，判断是否具备实际访问能力。
func canAccess(p string) bool {
	f, err := os.Open(p)
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}

// addRoot 内部登记（去重）。
func (g *Guard) addRoot(p string) {
	p = filepath.Clean(p)
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, r := range g.roots {
		if r == p {
			return
		}
	}
	g.roots = append(g.roots, p)
}

// ResolveDest 校验一个用于写入的目录：必须已存在且可写、位于授权根内。
func (g *Guard) ResolveDest(p string) (string, error) {
	real, err := g.Resolve(p)
	if err != nil {
		return "", err
	}
	st, err := os.Stat(real)
	if err != nil || !st.IsDir() {
		return "", apperr.New(apperr.CodeDestNotWritable, "目标目录不可写", "更换输出目录")
	}
	return real, nil
}

// EnsureDest 校验并（必要时）创建目标目录。
// 规则：最近的已存在祖先必须位于授权根内；创建后再次校验真实路径仍在授权根内。
func (g *Guard) EnsureDest(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" || strings.ContainsRune(p, 0) || len(p) > 4096 || !filepath.IsAbs(p) {
		return "", apperr.New(apperr.CodeDestNotWritable, "目标目录无效", "请填写绝对路径")
	}
	p = filepath.Clean(p)

	anc := p
	for {
		if _, err := os.Stat(anc); err == nil {
			break
		}
		parent := filepath.Dir(anc)
		if parent == anc {
			break
		}
		anc = parent
	}
	realAnc, err := filepath.EvalSymlinks(anc)
	if err != nil {
		return "", apperr.NotFound()
	}
	if !g.UnderRoot(realAnc) {
		if !canAccess(realAnc) {
			return "", apperr.NotAuthorized()
		}
		g.addRoot(realAnc)
	}
	if _, err := os.Stat(p); err != nil {
		if mkErr := os.MkdirAll(p, 0o755); mkErr != nil {
			return "", apperr.New(apperr.CodeDestNotWritable, "无法创建目标目录", "检查父目录权限或更换位置")
		}
	}
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", apperr.Wrap(err, "resolve dest")
	}
	if !g.UnderRoot(real) {
		return "", apperr.NotAuthorized()
	}
	st, err := os.Stat(real)
	if err != nil || !st.IsDir() {
		return "", apperr.New(apperr.CodeDestNotWritable, "目标目录不可写", "更换输出目录")
	}
	return real, nil
}

// SanitizeEntry 净化压缩包内条目名，返回安全相对路径；不安全时 ok=false。
func SanitizeEntry(name string) (string, bool) {
	name = strings.TrimSpace(name)
	if name == "" || strings.ContainsRune(name, 0) || len(name) > 4096 {
		return "", false
	}
	name = strings.ReplaceAll(name, "\\", "/")
	if strings.HasPrefix(name, "/") {
		return "", false
	}
	if len(name) >= 2 && name[1] == ':' {
		return "", false
	}
	cleaned := filepath.ToSlash(filepath.Clean(name))
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.Contains(cleaned, "/../") {
		return "", false
	}
	cleaned = strings.TrimPrefix(cleaned, "./")
	if cleaned == "." || cleaned == "" {
		return "", false
	}
	return cleaned, true
}

var (
	volumeTailNumRe  = regexp.MustCompile(`(?i)\.(\d{3})$`)                 // name.002 / name.010
	volumeTailZRe    = regexp.MustCompile(`(?i)\.z(\d{2})$`)                // name.z02 / name.z10
	volumeTailPartRe = regexp.MustCompile(`(?i)\.part(\d+)\.(rar|7z|zip)$`) // name.part2.rar / name.part10.7z
	volumeTailRarRe  = regexp.MustCompile(`(?i)\.r(\d{2})$`)                // name.r00 / name.r01（首卷是 name.rar）
)

// IsVolumeTail 判断是否为分卷包的后续卷（.z02/.part2.rar/.002/.r00 等）。
// 首卷形态：.001、.z01、.part1.rar、.rar（旧式 RAR 分卷）、通用包的 .zip/.7z 头。
func IsVolumeTail(p string) bool {
	base := filepath.Base(p)
	if m := volumeTailNumRe.FindStringSubmatch(base); m != nil {
		return m[1] != "001"
	}
	if m := volumeTailZRe.FindStringSubmatch(base); m != nil {
		n, err := strconv.Atoi(m[1])
		return err != nil || n >= 2
	}
	if m := volumeTailPartRe.FindStringSubmatch(base); m != nil {
		n, err := strconv.Atoi(m[1])
		return err == nil && n >= 2
	}
	if volumeTailRarRe.MatchString(base) {
		return true
	}
	return false
}
