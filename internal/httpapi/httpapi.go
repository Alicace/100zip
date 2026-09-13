// Package httpapi 提供 HTTP 接口（契约见 docs/06-接口与数据模型.md）。
// 统一响应：{"ok":true,"data":...} / {"ok":false,"error":{code,message,hint}}。
package httpapi

import (
	"archive/zip"
	"context"
	"encoding/binary"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/100zip/100zip/internal/apperr"
	"github.com/100zip/100zip/internal/codepage"
	"github.com/100zip/100zip/internal/disk"
	"github.com/100zip/100zip/internal/engine"
	"github.com/100zip/100zip/internal/jobs"
	"github.com/100zip/100zip/internal/paths"
	"github.com/100zip/100zip/internal/vault"
)

// Server 组装所有依赖。
type Server struct {
	Version string
	Engine  *engine.SevenZip
	Jobs    *jobs.Manager
	Guard   *paths.Guard
	DataDir string
	WwwDir  string
	Prefs   *PrefStore
	Vault   *vault.Vault
	mu      sync.Mutex
	listing map[string][]engine.Entry
}

// GatewayPrefix 是统一网关下的应用访问前缀（与 manifest/入口配置一致）。
const GatewayPrefix = "/app/100zip"

// Routes 返回挂载好的处理器。
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /api/capabilities", s.handleCapabilities)
	mux.HandleFunc("GET /api/context", s.handleContext)
	mux.HandleFunc("POST /api/archive/list", s.handleList)
	mux.HandleFunc("POST /api/archive/extract", s.handleExtract)
	mux.HandleFunc("POST /api/archive/compress", s.handleCompress)
	mux.HandleFunc("POST /api/archive/test", s.handleTest)
	mux.HandleFunc("GET /api/jobs", s.handleJobsList)
	mux.HandleFunc("GET /api/jobs/{id}", s.handleJobGet)
	mux.HandleFunc("POST /api/jobs/{id}/cancel", s.handleJobCancel)
	mux.HandleFunc("POST /api/jobs/{id}/password", s.handleJobPassword)
	mux.HandleFunc("DELETE /api/jobs/{id}", s.handleJobDelete)
	mux.HandleFunc("POST /api/jobs/clear-finished", s.handleJobsClearFinished)
	mux.HandleFunc("GET /api/prefs", s.handlePrefsGet)
	mux.HandleFunc("POST /api/prefs", s.handlePrefsSet)
	mux.HandleFunc("POST /api/authorize/register", s.handleAuthorizeRegister)
	mux.HandleFunc("GET /api/vault", s.handleVaultList)
	mux.HandleFunc("POST /api/vault", s.handleVaultAdd)
	mux.HandleFunc("GET /api/vault/{label}", s.handleVaultGet)
	mux.HandleFunc("DELETE /api/vault/{label}", s.handleVaultRemove)
	mux.HandleFunc("GET /api/diagnostics", s.handleDiagnostics)
	mux.HandleFunc("POST /api/archive/preview", s.handlePreview)
	mux.HandleFunc("POST /api/scan", s.handleScan)
	mux.HandleFunc("POST /api/archive/estimate", s.handleEstimate)
	if s.WwwDir != "" {
		mux.Handle("/", http.FileServer(http.Dir(s.WwwDir)))
	}
	return withRecover(stripGatewayPrefix(mux))
}

// stripGatewayPrefix 让同一套路由既能被网关（/app/100zip/...）访问，也能被本地直连访问。
func stripGatewayPrefix(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case p == GatewayPrefix:
			r.URL.Path = "/"
		case strings.HasPrefix(p, GatewayPrefix+"/"):
			r.URL.Path = strings.TrimPrefix(p, GatewayPrefix)
		}
		next.ServeHTTP(w, r)
	})
}

// ---------------------------------------------------------------- 响应助手

func ok(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": data})
}

func fail(w http.ResponseWriter, err error) {
	ae := apperr.Wrap(err, "")
	status := http.StatusInternalServerError
	switch ae.Code {
	case apperr.CodePathNotAuthorized, apperr.CodeACLDenied, apperr.CodeDestNotWritable:
		status = http.StatusForbidden
	case apperr.CodePathNotFound, apperr.CodeJobNotFound:
		status = http.StatusNotFound
	case apperr.CodePasswordRequired, apperr.CodePasswordWrong, apperr.CodeAuthRequired:
		status = http.StatusUnauthorized
	case apperr.CodeUnsupportedFormat:
		status = http.StatusUnsupportedMediaType
	case apperr.CodeArchiveCorrupt, apperr.CodeDestNotEmpty:
		status = http.StatusUnprocessableEntity
	case apperr.CodeDiskFull:
		status = http.StatusInsufficientStorage
	case apperr.CodeJobCancelled:
		status = http.StatusConflict
	case apperr.CodeExpansionLimit:
		status = http.StatusRequestEntityTooLarge
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if ae.Code == apperr.CodeInternal || ae.Detail != "" {
		slog.Warn("request failed", "code", string(ae.Code), "message", ae.Message, "detail", ae.Detail)
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok": false,
		"error": map[string]any{
			"code":    string(ae.Code),
			"message": ae.Message,
			"hint":    ae.Hint,
			"detail":  ae.Detail,
		},
	})
}

func withRecover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				fail(w, apperr.New(apperr.CodeInternal, "内部错误", "请提交诊断信息"))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// ---------------------------------------------------------------- 基础接口

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ok(w, map[string]any{"status": "ok", "version": s.Version})
}

func (s *Server) handleCapabilities(w http.ResponseWriter, r *http.Request) {
	caps, err := s.Engine.Capabilities(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{
		"engine":                caps.Engine,
		"engineVersion":         caps.Version,
		"path":                  caps.Path,
		"appVersion":            s.Version,
		"createFormats":         caps.CreateFormats,
		"extractFormats":        caps.ExtractFormats,
		"features":              caps.Features,
		"cpuCores":              runtime.NumCPU(),
		"recommendedConcurrent": minInt(runtime.NumCPU(), 8),
		"gpu": map[string]any{
			"supported": false,
			"reason":    "压缩与解压由 7-Zip/原生编解码器执行，当前版本不调用 GPU；并发数应按 CPU 核心和磁盘吞吐调整。",
		},
	})
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (s *Server) handleContext(w http.ResponseWriter, r *http.Request) {
	ok(w, map[string]any{
		"uid":               r.Header.Get("X-Trim-Userid"),
		"username":          r.Header.Get("X-Trim-Username"),
		"isAdmin":           strings.EqualFold(r.Header.Get("X-Trim-Isadmin"), "true"),
		"accessibleFolders": s.Guard.Roots(),
		"version": map[string]string{
			"app": s.Version,
			"os":  os.Getenv("TRIM_SYS_VERSION"),
		},
	})
}

// ---------------------------------------------------------------- 压缩包操作

type listReq struct {
	Path     string `json:"path"`
	Password string `json:"password"`
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	var req listReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fail(w, apperr.New(apperr.CodeInternal, "请求格式错误", ""))
		return
	}
	src, err := s.Guard.Resolve(req.Path)
	if err != nil {
		fail(w, err)
		return
	}
	entries, err := s.Engine.List(r.Context(), engine.ListRequest{Archive: src, Password: req.Password})
	if err != nil {
		fail(w, err)
		return
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Path)
	}
	det := codepage.Detect(names)
	s.rememberListing(src, entries)

	type listEntry struct {
		Index       int    `json:"index"`
		Path        string `json:"path"`
		DisplayPath string `json:"displayPath"`
		IsDir       bool   `json:"isDir"`
		Size        int64  `json:"size"`
		PackedSize  int64  `json:"packedSize"`
		Mtime       string `json:"mtime,omitempty"`
		Encrypted   bool   `json:"encrypted,omitempty"`
	}
	out := make([]listEntry, 0, len(entries))
	for i, e := range entries {
		display := e.Path
		if det.NeedsFix {
			if decoded, derr := codepage.Decode(e.Path, det.Codepage); derr == nil && decoded != "" {
				display = decoded
			}
		}
		out = append(out, listEntry{
			Index: i, Path: e.Path, DisplayPath: display, IsDir: e.IsDir,
			Size: e.Size, PackedSize: e.PackedSize, Mtime: e.Modified, Encrypted: e.Encrypted,
		})
	}
	ok(w, map[string]any{
		"total":         len(out),
		"entries":       out,
		"isVolume":      strings.HasSuffix(strings.ToLower(src), ".001"),
		"volume":        paths.AnalyzeVolumes(src),
		"encoding":      det,
		"needsPassword": hasEncrypted(entries),
	})
}

// rememberListing 缓存最近一次列表结果：把「条目序号」还原成引擎可匹配的原始名字。
// 原因：原始名字可能不是合法 UTF-8，经 JSON 传输会被替换字符破坏，因此前端只回传序号。
func (s *Server) rememberListing(archive string, entries []engine.Entry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listing == nil || len(s.listing) > 32 {
		s.listing = map[string][]engine.Entry{}
	}
	s.listing[archive] = entries
}

func (s *Server) cachedListing(archive string) []engine.Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listing[archive]
}

func hasEncrypted(entries []engine.Entry) bool {
	for _, e := range entries {
		if e.Encrypted {
			return true
		}
	}
	return false
}

// isOpenFailureCode 判断错误码是否属于「打不开压缩包」类，可尝试换入口（.z01）重试。
func isOpenFailureCode(c apperr.Code) bool {
	switch c {
	case apperr.CodeArchiveCorrupt, apperr.CodeUnsupportedFormat:
		return true
	}
	return false
}

// refineVolumeError 在路径确实属于分卷包时，把「包损坏」改写为更准确的「缺少分卷」。
//
// 依据（2026-09-12 实测，7-Zip 26.03，分离流验证：错误文本走 stderr，stdout 只有进度）：
// 缺中间卷或末卷时 stderr 统一为
// "Unexpected end of archive" / "Cannot open the file as [7z] archive"，
// 与真实损坏无法从文本区分；只有结合「该文件是分卷命名」这一上下文才能给出正确指引。
// ENGINE_FAILED 可能是超时/引擎启动失败，不属于「打不开」，不在此改写。
func refineVolumeError(err error, src string) error {
	ae, ok := err.(*apperr.Error)
	if !ok || ae.Code != apperr.CodeArchiveCorrupt {
		return err
	}
	if !paths.AnalyzeVolumes(src).IsVolume {
		return err
	}
	return apperr.New(apperr.CodeMissingVolume,
		"缺少分卷文件或分卷不完整",
		"请确认全部卷（.001/.002… / .z01… / .r00…）和首卷在同一目录；若刚下载完，请对照文件列表补齐缺失的分卷后重试")
}

type extractReq struct {
	Path         string   `json:"path"`
	Dest         string   `json:"dest"`
	Entries      []string `json:"entries"`
	EntryIndexes []int    `json:"entryIndexes"`
	Password     string   `json:"password"`
	VaultLabel   string   `json:"vaultLabel"`
	DestMode     string   `json:"destMode"`   // here | subdir | custom
	AutoDelete   bool     `json:"autoDelete"` // 解压成功后删除源压缩包（危险操作，需前端显式确认）
	TryVault     bool     `json:"tryVault"`   // 失败时自动尝试密码库中的其它密码
	Overwrite    string   `json:"overwrite"`
	CreateSubdir bool     `json:"createSubdir"`
	FixEncoding  bool     `json:"fixEncoding"`
}

func (s *Server) handleExtract(w http.ResponseWriter, r *http.Request) {
	var req extractReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fail(w, apperr.New(apperr.CodeInternal, "请求格式错误", ""))
		return
	}
	src, err := s.Guard.Resolve(req.Path)
	if err != nil {
		fail(w, err)
		return
	}
	if paths.IsVolumeTail(src) {
		fail(w, apperr.New(apperr.CodePathIsVolumeTail, "这是分卷包的后续卷", "请选择首卷（如 .7z.001）"))
		return
	}
	// 缺卷检测：只下了一半就解压是最常见的坑，这里给出明确提示而不是「包损坏」
	if vol := paths.AnalyzeVolumes(src); vol.IsVolume && len(vol.Missing) > 0 {
		fail(w, apperr.New(apperr.CodeMissingVolume,
			fmt.Sprintf("缺少分卷文件：%s", strings.Join(vol.Missing, "、")),
			"请把该压缩包的所有分卷放在同一目录后重试（.002/.003 或 .z02/.r00）"))
		return
	}
	// 密码来源：显式密码优先；否则从密码库按标签取（明文不经过前端）
	if req.Password == "" && req.VaultLabel != "" && s.Vault != nil {
		if pw, found := s.Vault.Get(req.VaultLabel); found {
			req.Password = pw
		}
	}
	// 目标目录：
	//   here   → 压缩包所在目录
	//   subdir → 压缩包所在目录/包名 子目录
	//   custom → 用户指定（可再叠加包名子目录）
	destBase := req.Dest
	if req.DestMode == "here" || req.DestMode == "subdir" {
		destBase = filepath.Dir(src)
	}
	dest, err := s.Guard.EnsureDest(destBase)
	if err != nil {
		fail(w, err)
		return
	}
	subdirName := strings.TrimSuffix(filepath.Base(src), filepath.Ext(src))
	if req.DestMode == "subdir" {
		// 批量解压时，不同格式可能同名（例如 data.rar 与 data.zip）。
		// 避免它们共用目录，必要时按扩展名和序号生成隔离目录。
		base := filepath.Join(dest, subdirName)
		candidate := base
		if _, statErr := os.Stat(candidate); statErr == nil {
			extLabel := strings.TrimPrefix(strings.ToLower(filepath.Ext(src)), ".")
			if extLabel != "" {
				candidate = base + " (" + extLabel + ")"
			}
			for i := 2; ; i++ {
				if _, statErr = os.Stat(candidate); os.IsNotExist(statErr) {
					break
				}
				candidate = fmt.Sprintf("%s (%d)", base, i)
			}
		}
		dest = candidate
	} else if req.CreateSubdir {
		dest = filepath.Join(dest, subdirName)
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		fail(w, apperr.New(apperr.CodeDestNotWritable, "无法创建目标目录", "更换输出目录"))
		return
	}
	destFinal := dest

	selected := req.Entries
	if len(req.EntryIndexes) > 0 {
		if cached := s.cachedListing(src); len(cached) > 0 {
			selected = make([]string, 0, len(req.EntryIndexes))
			for _, idx := range req.EntryIndexes {
				if idx >= 0 && idx < len(cached) {
					selected = append(selected, cached[idx].Path)
				}
			}
		}
	}

	// 持久化续跑参数（密码不落盘）：waiting/manual 状态下用户输入密码后可重建 runner
	storedReq := req
	storedReq.Password = ""
	params := map[string]any{
		"entries":     len(selected),
		"overwrite":   req.Overwrite,
		"fixEncoding": req.FixEncoding,
		"destFinal":   destFinal,
		"selected":    selected,
	}
	if raw, merr := json.Marshal(storedReq); merr == nil {
		params["extractReq"] = string(raw)
	}

	job := s.Jobs.Enqueue(&jobs.Job{
		Type:     "extract",
		Title:    "解压 " + filepath.Base(src),
		SrcPath:  src,
		DestPath: destFinal,
		Params:   params,
	}, s.buildExtractRunner(req, src, destFinal, selected))
	ok(w, map[string]any{"jobId": job.ID})
}

// isPasswordCode 判断是否密码类错误（此类错误转「等待密码」而不是失败）。
func isPasswordCode(c apperr.Code) bool {
	return c == apperr.CodePasswordRequired || c == apperr.CodePasswordWrong
}

// buildExtractRunner 按请求构造解压任务闭包；创建与「密码续跑」共用，
// 续跑时 src/destFinal/selected 来自任务 Params（已过 Guard 校验，无需重复解析）。
func (s *Server) buildExtractRunner(req extractReq, src, destFinal string, selected []string) jobs.TaskFunc {
	return func(ctx context.Context, j *jobs.Job, report func(jobs.Progress, string)) error {
		extractArchive := func(archive, pw string) error {
			return s.Engine.Extract(ctx, engine.ExtractRequest{
				Archive:   archive,
				Dest:      destFinal,
				Entries:   selected,
				Password:  pw,
				Overwrite: req.Overwrite,
			}, func(p engine.Progress) {
				report(jobs.Progress{Percent: p.Percent, Bytes: p.Bytes, Files: p.Files}, "")
			})
		}
		doExtract := func(pw string) error {
			err := extractArchive(src, pw)
			if err == nil {
				return nil
			}
			// WinRAR 风格 zip 分卷（data.zip + data.z01…）：直接打开 .zip 失败时，
			// 改用 .z01 作为入口重试一次——7-Zip 打开 .z01 时会主动去找同名 .zip。
			if ae, isApp := err.(*apperr.Error); isApp && isOpenFailureCode(ae.Code) {
				if alt := paths.Z01ForZipHead(src); alt != "" {
					report(jobs.Progress{Percent: 0}, "尝试以 .z01 首卷重新打开…")
					if retryErr := extractArchive(alt, pw); retryErr == nil {
						return nil
					}
				}
			}
			return refineVolumeError(err, src)
		}
		err := doExtract(req.Password)
		// 密码词典：失败且为密码问题时，依次尝试密码库中的其它密码
		if err != nil && req.TryVault && s.Vault != nil {
			if ae, isApp := err.(*apperr.Error); isApp && isPasswordCode(ae.Code) {
				for _, item := range s.Vault.List() {
					if item.Label == req.VaultLabel {
						continue
					}
					pw, found := s.Vault.Get(item.Label)
					if !found {
						continue
					}
					report(jobs.Progress{Percent: 0}, "尝试密码库密码："+item.Label)
					if retryErr := doExtract(pw); retryErr == nil {
						report(jobs.Progress{Percent: 100}, "密码命中："+item.Label)
						err = nil
						break
					}
				}
			}
		}
		if err != nil {
			// 密码类错误：不判失败，转「等待密码」由用户补密码后继续
			if ae, isApp := err.(*apperr.Error); isApp && isPasswordCode(ae.Code) {
				j.NeedsPassword = true
				j.State = jobs.StateWaiting
				return nil
			}
			return err
		}
		if req.FixEncoding {
			det := codepage.DetectDir(destFinal)
			if det.NeedsFix {
				plan, perr := codepage.PlanRename(destFinal, det.Codepage)
				if perr == nil && len(plan) > 0 {
					if n, aerr := codepage.ApplyRename(plan); aerr == nil && n > 0 {
						report(jobs.Progress{Percent: 100, Files: n}, "已修复文件名编码")
					}
				}
			}
		}
		if req.AutoDelete {
			if rmErr := os.Remove(src); rmErr == nil {
				report(jobs.Progress{Percent: 100}, "已删除源压缩包")
			} else {
				report(jobs.Progress{Percent: 100}, "源压缩包删除失败："+rmErr.Error())
			}
		}
		return nil
	}
}

// ---------------------------------------------------------------- 任务密码续跑

type jobPasswordReq struct {
	Password string `json:"password"`
	Action   string `json:"action"` // resume | defer
	Save     bool   `json:"save"`
	Label    string `json:"label,omitempty"`
}

func (s *Server) handleJobPassword(w http.ResponseWriter, r *http.Request) {
	var req jobPasswordReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fail(w, apperr.New(apperr.CodeInternal, "请求格式错误", ""))
		return
	}
	id := r.PathValue("id")
	j, found := s.Jobs.Get(id)
	if !found {
		fail(w, apperr.New(apperr.CodeJobNotFound, "任务不存在或已被清理", ""))
		return
	}
	if j.State != jobs.StateWaiting && j.State != jobs.StateManual {
		fail(w, apperr.New(apperr.CodeJobNotFound, "该任务不在等待密码状态", ""))
		return
	}
	if req.Action == "defer" {
		if !s.Jobs.Defer(id) {
			fail(w, apperr.New(apperr.CodeJobNotFound, "任务状态已变化，请刷新", ""))
			return
		}
		ok(w, map[string]any{"state": jobs.StateManual})
		return
	}
	if strings.TrimSpace(req.Password) == "" {
		fail(w, apperr.New(apperr.CodePasswordRequired, "请输入密码", ""))
		return
	}
	raw, _ := j.Params["extractReq"].(string)
	if raw == "" {
		fail(w, apperr.New(apperr.CodeInternal, "该任务缺少可续跑的参数", "请删除后重新发起任务"))
		return
	}
	var req0 extractReq
	if err := json.Unmarshal([]byte(raw), &req0); err != nil {
		fail(w, apperr.New(apperr.CodeInternal, "任务参数损坏，无法续跑", "请删除后重新发起任务"))
		return
	}
	destFinal, _ := j.Params["destFinal"].(string)
	selected := parseStringSlice(j.Params["selected"])
	req0.Password = req.Password
	if req.Save && s.Vault != nil {
		label := strings.TrimSpace(req.Label)
		if label == "" {
			label = filepath.Base(j.SrcPath)
		}
		_ = s.Vault.Add(label, req.Password)
	}
	fn := s.buildExtractRunner(req0, j.SrcPath, destFinal, selected)
	if !s.Jobs.Requeue(id, fn) {
		fail(w, apperr.New(apperr.CodeJobNotFound, "任务状态已变化，请刷新", ""))
		return
	}
	ok(w, map[string]any{"jobId": id, "state": jobs.StateQueued})
}

// parseStringSlice 还原 Params 里存的字符串数组（持久化历史导致可能是 []any）。
func parseStringSlice(v any) []string {
	switch arr := v.(type) {
	case []string:
		return arr
	case []any:
		out := make([]string, 0, len(arr))
		for _, item := range arr {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// ---------------------------------------------------------------- 目录扫描

type scanReq struct {
	Path         string `json:"path"`
	Recursive    bool   `json:"recursive"`
	ArchivesOnly bool   `json:"archivesOnly"`
}

var archiveExts = map[string]bool{
	".7z": true, ".zip": true, ".rar": true, ".tar": true, ".tgz": true, ".tbz": true,
	".tbz2": true, ".txz": true, ".gz": true, ".bz2": true, ".xz": true, ".zst": true,
	".zstd": true, ".lzma": true, ".cab": true, ".iso": true, ".arj": true,
	".lzh": true, ".lha": true, ".001": true, ".tzst": true,
}

// isFirstVolume 判断是否为分卷首卷（只处理首卷，避免重复解压）。
func isFirstVolume(name string) bool {
	l := strings.ToLower(name)
	if strings.HasSuffix(l, ".001") || strings.HasSuffix(l, ".z01") {
		return true
	}
	if strings.Contains(l, ".part1.") || strings.Contains(l, ".part01.") {
		return true
	}
	if strings.HasSuffix(l, ".rar") {
		// .rar 可能是旧式分卷首卷，交给引擎处理
		return true
	}
	return false
}

func (s *Server) handleScan(w http.ResponseWriter, r *http.Request) {
	var req scanReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fail(w, apperr.New(apperr.CodeInternal, "请求格式错误", ""))
		return
	}
	root, err := s.Guard.Resolve(req.Path)
	if err != nil {
		fail(w, err)
		return
	}
	type fileItem struct {
		Path      string   `json:"path"`
		Name      string   `json:"name"`
		Size      int64    `json:"size"`
		Mtime     string   `json:"mtime"`
		Kind      string   `json:"kind"`
		Volume    bool     `json:"volume"`
		Total     int      `json:"total,omitempty"`
		Encrypted *bool    `json:"encrypted"`
		Missing   []string `json:"missing,omitempty"`
	}
	items := make([]fileItem, 0, 64)
	walkFn := func(p string, info os.FileInfo, werr error) error {
		if werr != nil || info == nil {
			return nil
		}
		if info.IsDir() {
			// 非归档扫描时输出目录条目（供压缩来源的树状浏览对话框使用）
			// 注意：必须先输出条目再 SkipDir——SkipDir 会立即返回，写在后面永远执行不到
			if !req.ArchivesOnly && p != root {
				items = append(items, fileItem{
					Path:  p,
					Name:  filepath.Base(p),
					Mtime: info.ModTime().Format(time.RFC3339),
					Kind:  "dir",
				})
			}
			if !req.Recursive && p != root {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(p))
		isArchive := archiveExts[ext]
		if req.ArchivesOnly && !isArchive {
			return nil
		}
		if isArchive {
			// 分卷：只保留首卷，并把缺卷信息带上（批量页可提示）
			if vol := paths.AnalyzeVolumes(p); vol.IsVolume {
				if vol.First != "" && filepath.Base(p) != vol.First {
					return nil // 后续卷跳过
				}
				item := fileItem{
					Path: p, Name: filepath.Base(p), Size: info.Size(),
					Mtime: info.ModTime().Format(time.RFC3339),
					Kind:  "archive", Volume: true, Total: vol.Total, Missing: vol.Missing,
				}
				items = append(items, item)
				return nil
			}
		}
		// 非压缩包扩展名、但属于某个分卷包成员的（.002/.z01/.r00…）：不单独展示，避免误勾选
		if !isArchive && paths.IsVolumeMember(p) {
			return nil
		}
		item := fileItem{
			Path: p, Name: filepath.Base(p), Size: info.Size(),
			Mtime:  info.ModTime().Format(time.RFC3339),
			Kind:   map[bool]string{true: "archive", false: "file"}[isArchive],
			Volume: isFirstVolume(filepath.Base(p)),
		}
		if ext == ".zip" {
			if enc, ok := zipEncrypted(p); ok {
				item.Encrypted = &enc
			}
		}
		items = append(items, item)
		return nil
	}
	if err := filepath.Walk(root, walkFn); err != nil {
		fail(w, apperr.Wrap(err, "扫描目录"))
		return
	}
	ok(w, map[string]any{"root": root, "total": len(items), "files": items})
}

// zipEncrypted 轻量检测 ZIP 是否加密（读取中央目录的加密标志位）。
func zipEncrypted(path string) (bool, bool) {
	f, err := os.Open(path)
	if err != nil {
		return false, false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.Size() < 22 {
		return false, false
	}
	// 读取尾部 64KB 查找 EOCD
	tailSize := int64(64 * 1024)
	if st.Size() < tailSize {
		tailSize = st.Size()
	}
	buf := make([]byte, tailSize)
	if _, err := f.ReadAt(buf, st.Size()-tailSize); err != nil {
		return false, false
	}
	idx := -1
	for i := len(buf) - 22; i >= 0; i-- {
		if buf[i] == 0x50 && buf[i+1] == 0x4b && buf[i+2] == 0x05 && buf[i+3] == 0x06 {
			idx = i
			break
		}
	}
	if idx < 0 || idx+22 > len(buf) {
		return false, false
	}
	cdOffset := int64(binary.LittleEndian.Uint32(buf[idx+16 : idx+20]))
	cdSize := int64(binary.LittleEndian.Uint32(buf[idx+12 : idx+16]))
	if cdSize == 0 || cdSize > 64*1024*1024 {
		return false, false
	}
	cd := make([]byte, cdSize)
	if _, err := f.ReadAt(cd, cdOffset); err != nil {
		return false, false
	}
	for i := 0; i+46 <= len(cd); {
		if cd[i] != 0x50 || cd[i+1] != 0x4b || cd[i+2] != 0x01 || cd[i+3] != 0x02 {
			break
		}
		flags := binary.LittleEndian.Uint16(cd[i+8 : i+10])
		if flags&0x0001 != 0 {
			return true, true
		}
		nameLen := int(binary.LittleEndian.Uint16(cd[i+28 : i+30]))
		extraLen := int(binary.LittleEndian.Uint16(cd[i+30 : i+32]))
		commentLen := int(binary.LittleEndian.Uint16(cd[i+32 : i+34]))
		i += 46 + nameLen + extraLen + commentLen
	}
	return false, true
}

type compressReq struct {
	Sources      []string `json:"sources"`
	Dest         string   `json:"dest"`
	Format       string   `json:"format"`
	Level        int      `json:"level"`
	Password     string   `json:"password"`
	VaultLabel   string   `json:"vaultLabel"`
	SplitSize    string   `json:"splitSize"`
	Method       string   `json:"method"`
	Solid        bool     `json:"solid"`
	Dictionary   string   `json:"dictionary"`
	TestAfter    bool     `json:"testAfter"`
	DeleteSource bool     `json:"deleteSource"`
}

func (s *Server) handleCompress(w http.ResponseWriter, r *http.Request) {
	var req compressReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fail(w, apperr.New(apperr.CodeInternal, "请求格式错误", ""))
		return
	}
	if len(req.Sources) == 0 {
		fail(w, apperr.New(apperr.CodeInternal, "请至少选择一个来源", ""))
		return
	}
	if req.Password == "" && req.VaultLabel != "" && s.Vault != nil {
		if pw, found := s.Vault.Get(req.VaultLabel); found {
			req.Password = pw
		}
	}
	sources := make([]string, 0, len(req.Sources))
	for _, p := range req.Sources {
		real, err := s.Guard.Resolve(p)
		if err != nil {
			fail(w, err)
			return
		}
		sources = append(sources, real)
	}
	destDir, err := s.Guard.ResolveDest(filepath.Dir(req.Dest))
	if err != nil {
		fail(w, err)
		return
	}
	dest := filepath.Join(destDir, filepath.Base(req.Dest))
	job := s.Jobs.Enqueue(&jobs.Job{
		Type:     "compress",
		Title:    "压缩 → " + filepath.Base(dest),
		DestPath: dest,
		Params:   map[string]any{"format": req.Format, "level": req.Level, "split": req.SplitSize, "method": req.Method, "dictionary": req.Dictionary, "solid": req.Solid, "testAfter": req.TestAfter, "deleteSource": req.DeleteSource},
	}, func(ctx context.Context, j *jobs.Job, report func(jobs.Progress, string)) error {
		creq := engine.CompressRequest{
			Sources:    sources,
			Dest:       dest,
			Format:     req.Format,
			Level:      req.Level,
			Password:   req.Password,
			SplitSize:  req.SplitSize,
			Method:     req.Method,
			Solid:      req.Solid,
			Dictionary: req.Dictionary,
		}
		onP := func(p engine.Progress) {
			report(jobs.Progress{Percent: p.Percent}, "")
		}
		// 纯 Go 编解码格式（zst/lz4/br 及其 tar 变体）走原生实现
		var compressErr error
		if _, isNative := engine.ParseNativeFormat(req.Format); isNative {
			if req.Password != "" {
				return apperr.New(apperr.CodeUnsupportedFormat, "该格式暂不支持加密", "请改用 7z 或 zip 加密")
			}
			compressErr = s.Engine.CompressNative(ctx, creq, onP)
		} else {
			compressErr = s.Engine.Compress(ctx, creq, onP)
		}
		if compressErr != nil {
			return compressErr
		}
		if req.TestAfter {
			testArchive := dest
			if req.SplitSize != "" && (strings.EqualFold(req.Format, "7z") || strings.EqualFold(req.Format, "zip")) {
				testArchive += ".001"
			}
			if err := s.Engine.Test(ctx, engine.TestRequest{Archive: testArchive, Password: req.Password}); err != nil {
				return err
			}
		}
		if req.DeleteSource {
			for _, srcPath := range sources {
				if err := os.RemoveAll(srcPath); err != nil {
					return apperr.Wrap(err, "压缩成功但删除源文件失败")
				}
			}
		}
		return nil
	})
	ok(w, map[string]any{"jobId": job.ID, "dest": dest})
}

type testReq struct {
	Path     string `json:"path"`
	Password string `json:"password"`
}

func (s *Server) handleTest(w http.ResponseWriter, r *http.Request) {
	var req testReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fail(w, apperr.New(apperr.CodeInternal, "请求格式错误", ""))
		return
	}
	src, err := s.Guard.Resolve(req.Path)
	if err != nil {
		fail(w, err)
		return
	}
	job := s.Jobs.Enqueue(&jobs.Job{Type: "test", Title: "完整性测试 " + filepath.Base(src), SrcPath: src}, func(ctx context.Context, j *jobs.Job, report func(jobs.Progress, string)) error {
		if err := s.Engine.Test(ctx, engine.TestRequest{Archive: src, Password: req.Password}); err != nil {
			return refineVolumeError(err, src)
		}
		return nil
	})
	ok(w, map[string]any{"jobId": job.ID})
}

// ---------------------------------------------------------------- 任务接口

func (s *Server) handleJobsList(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	ok(w, map[string]any{"jobs": s.Jobs.List(limit)})
}

func (s *Server) handleJobGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	j, found := s.Jobs.Get(id)
	if !found {
		fail(w, apperr.New(apperr.CodeJobNotFound, "任务不存在或已被清理", ""))
		return
	}
	ok(w, j)
}

func (s *Server) handleJobCancel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.Jobs.Cancel(id) {
		fail(w, apperr.New(apperr.CodeJobNotFound, "任务不存在或已结束", ""))
		return
	}
	ok(w, map[string]any{"cancelled": true})
}

// handleJobDelete 删除一条已结束的任务记录。
func (s *Server) handleJobDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.Jobs.Delete(id) {
		fail(w, apperr.New(apperr.CodeJobNotFound, "任务不存在或仍在运行", "运行中的任务请先取消"))
		return
	}
	ok(w, map[string]any{"deleted": id})
}

// handleJobsClearFinished 清空所有已结束任务。
func (s *Server) handleJobsClearFinished(w http.ResponseWriter, r *http.Request) {
	ok(w, map[string]any{"removed": s.Jobs.ClearFinished()})
}

// ---------------------------------------------------------------- 密码库

func (s *Server) handleVaultList(w http.ResponseWriter, r *http.Request) {
	items := []vault.Item{}
	if s.Vault != nil {
		items = s.Vault.List()
	}
	ok(w, map[string]any{"items": items})
}

type vaultAddReq struct {
	Label    string `json:"label"`
	Password string `json:"password"`
}

func (s *Server) handleVaultAdd(w http.ResponseWriter, r *http.Request) {
	if s.Vault == nil {
		fail(w, apperr.New(apperr.CodeInternal, "密码库不可用", ""))
		return
	}
	var req vaultAddReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fail(w, apperr.New(apperr.CodeInternal, "请求格式错误", ""))
		return
	}
	if err := s.Vault.Add(req.Label, req.Password); err != nil {
		fail(w, apperr.Wrap(err, "保存密码失败"))
		return
	}
	ok(w, map[string]any{"saved": req.Label})
}

func (s *Server) handleVaultRemove(w http.ResponseWriter, r *http.Request) {
	if s.Vault == nil {
		fail(w, apperr.New(apperr.CodeInternal, "密码库不可用", ""))
		return
	}
	label := r.PathValue("label")
	if err := s.Vault.Remove(label); err != nil {
		fail(w, apperr.Wrap(err, "删除密码失败"))
		return
	}
	ok(w, map[string]any{"removed": label})
}

func (s *Server) handleVaultGet(w http.ResponseWriter, r *http.Request) {
	if s.Vault == nil {
		fail(w, apperr.New(apperr.CodeInternal, "密码库不可用", ""))
		return
	}
	label := r.PathValue("label")
	pw, found := s.Vault.Get(label)
	if !found {
		fail(w, apperr.New(apperr.CodeJobNotFound, "密码不存在或已删除", ""))
		return
	}
	ok(w, map[string]any{"label": label, "password": pw})
}

// ---------------------------------------------------------------- 诊断报告

// handleDiagnostics 生成脱敏诊断报告（不含任何明文密码）。
func (s *Server) handleDiagnostics(w http.ResponseWriter, r *http.Request) {
	caps, _ := s.Engine.Capabilities(r.Context())
	recent := s.Jobs.List(20)
	jobsBrief := make([]map[string]any, 0, len(recent))
	for _, j := range recent {
		jobsBrief = append(jobsBrief, map[string]any{
			"id": j.ID, "type": j.Type, "title": j.Title, "state": j.State,
			"src": j.SrcPath, "dest": j.DestPath, "error": j.Error,
			"params": j.Params, "logTail": j.LogTail,
			"srcMeta": diagnosticFileMeta(j.SrcPath), "destMeta": diagnosticFileMeta(j.DestPath),
			"createdAt": j.CreatedAt.Format(time.RFC3339),
		})
	}
	ok(w, map[string]any{
		"generatedAt": time.Now().Format(time.RFC3339),
		"app": map[string]any{
			"version":       s.Version,
			"enginePath":    caps.Path,
			"engineVersion": caps.Version,
			"createFormats": caps.CreateFormats,
		},
		"system": map[string]string{
			"fnosVersion": os.Getenv("TRIM_SYS_VERSION"),
			"arch":        os.Getenv("TRIM_SYS_ARCH"),
			"kernel":      os.Getenv("TRIM_KERNEL_VERSION"),
		},
		"authorizedFolders": s.Guard.Roots(),
		"prefs":             s.Prefs.get(),
		"recentJobs":        jobsBrief,
		"note":              "本报告已脱敏：不含密码明文；路径为用户自己的授权目录，用于问题排查。",
	})
}

func diagnosticFileMeta(path string) map[string]any {
	if path == "" {
		return nil
	}
	m := map[string]any{"path": path, "format": strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")}
	if st, err := os.Stat(path); err == nil {
		m["exists"] = true
		m["isDir"] = st.IsDir()
		m["size"] = st.Size()
		m["mtime"] = st.ModTime().Format(time.RFC3339)
	} else {
		m["exists"] = false
		m["error"] = err.Error()
	}
	return m
}

// ---------------------------------------------------------------- 容量预估

type estimateReq struct {
	Operation string   `json:"operation"` // extract | compress
	Path      string   `json:"path"`
	Paths     []string `json:"paths"`
	Dest      string   `json:"dest"`
	Password  string   `json:"password"`
}

func (s *Server) handleEstimate(w http.ResponseWriter, r *http.Request) {
	var req estimateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fail(w, apperr.New(apperr.CodeInternal, "请求格式错误", ""))
		return
	}
	if req.Dest == "" {
		fail(w, apperr.New(apperr.CodeDestNotWritable, "请先选择输出位置", ""))
		return
	}
	dest, err := s.Guard.ResolveDest(req.Dest)
	if err != nil {
		fail(w, err)
		return
	}
	var sourceBytes int64
	var details []map[string]any
	switch strings.ToLower(req.Operation) {
	case "extract":
		paths := req.Paths
		if req.Path != "" {
			paths = append(paths, req.Path)
		}
		if len(paths) == 0 {
			fail(w, apperr.New(apperr.CodeInternal, "缺少压缩包路径", ""))
			return
		}
		for _, p := range paths {
			src, e := s.Guard.Resolve(p)
			if e != nil {
				fail(w, e)
				return
			}
			entries, e := s.Engine.List(r.Context(), engine.ListRequest{Archive: src, Password: req.Password})
			if e != nil {
				fail(w, e)
				return
			}
			var n int64
			for _, x := range entries {
				if !x.IsDir && x.Size > 0 {
					n += x.Size
				}
			}
			sourceBytes += n
			details = append(details, map[string]any{"path": src, "outputBytes": n, "entries": len(entries)})
		}
	case "compress":
		if len(req.Paths) == 0 {
			fail(w, apperr.New(apperr.CodeInternal, "缺少压缩来源", ""))
			return
		}
		for _, p := range req.Paths {
			src, e := s.Guard.Resolve(p)
			if e != nil {
				fail(w, e)
				return
			}
			var n int64
			e = filepath.Walk(src, func(path string, info os.FileInfo, werr error) error {
				if werr != nil {
					return werr
				}
				if info != nil && !info.IsDir() {
					n += info.Size()
				}
				return nil
			})
			if e != nil {
				fail(w, apperr.Wrap(e, "统计压缩来源"))
				return
			}
			sourceBytes += n
			details = append(details, map[string]any{"path": src, "sourceBytes": n})
		}
	default:
		fail(w, apperr.New(apperr.CodeInternal, "未知预估类型", ""))
		return
	}
	// 解压按实际条目总大小预留 5% 余量；压缩按源数据大小预留 10% 余量。
	margin := int64(256 * 1024 * 1024)
	rate := int64(10)
	if strings.EqualFold(req.Operation, "extract") {
		rate = 20
	}
	if sourceBytes/rate > margin {
		margin = sourceBytes / rate
	}
	estimate := sourceBytes + margin
	free, freeErr := disk.FreeBytes(dest)
	sufficient := freeErr == nil && uint64(estimate) <= free
	ok(w, map[string]any{"operation": req.Operation, "dest": dest, "sourceBytes": sourceBytes, "estimatedOutputBytes": estimate, "marginBytes": margin, "freeBytes": free, "freeKnown": freeErr == nil, "sufficient": sufficient, "details": details})
}

// ---------------------------------------------------------------- 包内预览

type previewReq struct {
	Path       string `json:"path"`
	EntryIndex int    `json:"entryIndex"`
	Password   string `json:"password"`
	VaultLabel string `json:"vaultLabel"`
}

// handlePreview 取出压缩包中的单个条目用于预览：
//   - 图片：直接返回二进制（Content-Type 由内容嗅探）
//   - 文本类：返回 JSON {kind:"text", content}
//   - 其它：返回 JSON {kind:"unsupported"}
func (s *Server) handlePreview(w http.ResponseWriter, r *http.Request) {
	var req previewReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fail(w, apperr.New(apperr.CodeInternal, "请求格式错误", ""))
		return
	}
	src, err := s.Guard.Resolve(req.Path)
	if err != nil {
		fail(w, err)
		return
	}
	if req.Password == "" && req.VaultLabel != "" && s.Vault != nil {
		if pw, found := s.Vault.Get(req.VaultLabel); found {
			req.Password = pw
		}
	}
	cached := s.cachedListing(src)
	if req.EntryIndex < 0 || req.EntryIndex >= len(cached) {
		fail(w, apperr.New(apperr.CodeJobNotFound, "条目不存在，请重新打开压缩包", ""))
		return
	}
	entry := cached[req.EntryIndex]
	if entry.IsDir {
		fail(w, apperr.New(apperr.CodeUnsupportedFormat, "目录无法预览", ""))
		return
	}

	tmp, err := os.MkdirTemp("", "100zip-preview-")
	if err != nil {
		fail(w, apperr.Wrap(err, "创建临时目录"))
		return
	}
	defer os.RemoveAll(tmp)

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	if err := s.Engine.Extract(ctx, engine.ExtractRequest{
		Archive:  src,
		Dest:     tmp,
		Entries:  []string{entry.Path},
		Password: req.Password,
	}, nil); err != nil {
		fail(w, refineVolumeError(err, src))
		return
	}

	var outPath string
	_ = filepath.Walk(tmp, func(p string, info os.FileInfo, werr error) error {
		if werr == nil && info != nil && !info.IsDir() && outPath == "" {
			outPath = p
		}
		return nil
	})
	// 部分 zip/7z 条目记录的权限位可能为 0，7zzs 解出后属主自己也读不了
	// （真机实测：open ... permission denied）。预览属临时文件，先放开权限再打开。
	_ = os.Chmod(outPath, 0o644)
	if outPath == "" {
		fail(w, apperr.New(apperr.CodeArchiveCorrupt, "无法取出该文件", "可能受密码保护或包已损坏"))
		return
	}

	f, err := os.Open(outPath)
	if err != nil {
		fail(w, apperr.Wrap(err, "打开解出的文件"))
		return
	}
	defer f.Close()

	ext := strings.ToLower(filepath.Ext(entry.Path))
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".svg", ".ico":
		head := make([]byte, 512)
		n, _ := f.Read(head)
		w.Header().Set("Content-Type", http.DetectContentType(head[:n]))
		w.Header().Set("Cache-Control", "no-store")
		_, _ = f.Seek(0, io.SeekStart)
		_, _ = io.Copy(w, f)
	case ".txt", ".md", ".json", ".xml", ".log", ".csv", ".ini", ".yml", ".yaml", ".srt", ".ass", ".nfo", ".html", ".htm", ".css", ".js", ".ts", ".py", ".go", ".java", ".c", ".cpp", ".h", ".sql", ".sh", ".bat":
		const maxRead = 256 * 1024
		buf := make([]byte, maxRead)
		n, _ := io.ReadFull(f, buf)
		content := string(buf[:n])
		if !utf8.ValidString(content) {
			if decoded, derr := codepage.Decode(content, "gbk"); derr == nil {
				content = decoded
			}
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"data": map[string]any{
				"kind": "text", "content": content, "truncated": int64(n) >= maxRead,
			},
		})
	case ".mp3", ".wav", ".flac", ".ogg", ".oga", ".m4a", ".aac", ".opus", ".mp4", ".webm", ".mov", ".mkv", ".avi", ".m4v", ".pdf":
		ctype := map[string]string{
			".mp3": "audio/mpeg", ".wav": "audio/wav", ".flac": "audio/flac", ".ogg": "audio/ogg", ".oga": "audio/ogg",
			".m4a": "audio/mp4", ".aac": "audio/aac", ".opus": "audio/opus", ".mp4": "video/mp4", ".webm": "video/webm",
			".mov": "video/quicktime", ".mkv": "video/x-matroska", ".avi": "video/x-msvideo", ".m4v": "video/mp4", ".pdf": "application/pdf",
		}[ext]
		if ctype == "" {
			ctype = "application/octet-stream"
		}
		if st, serr := f.Stat(); serr == nil {
			w.Header().Set("Content-Length", strconv.FormatInt(st.Size(), 10))
		}
		w.Header().Set("Content-Type", ctype)
		w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=%q", filepath.Base(entry.Path)))
		w.Header().Set("Cache-Control", "no-store")
		_, _ = io.Copy(w, f)
	case ".docx", ".xlsx", ".pptx":
		kind, content, err := officeTextPreview(outPath)
		if err != nil {
			fail(w, apperr.Wrap(err, "读取 Office 文档"))
			return
		}
		ok(w, map[string]any{"kind": kind, "content": content, "truncated": len(content) >= 256*1024})
	default:
		var size int64
		if fi, serr := f.Stat(); serr == nil {
			size = fi.Size()
		}
		ok(w, map[string]any{
			"kind":    "unsupported",
			"message": fmt.Sprintf("暂不支持预览 %s 格式（%.2f MB）", ext, float64(size)/1024/1024),
		})
	}
}

// officeTextPreview 提取 OOXML 文档中的可读文字，适合快速预览；不承诺还原排版。
func officeTextPreview(path string) (string, string, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return "", "", err
	}
	defer zr.Close()
	var names []string
	for _, f := range zr.File {
		n := strings.ToLower(f.Name)
		if strings.HasSuffix(n, ".xml") && (strings.Contains(n, "document") || strings.Contains(n, "sharedstrings") || strings.Contains(n, "slide") || strings.Contains(n, "sheet")) {
			names = append(names, f.Name)
		}
	}
	sort.Strings(names)
	var b strings.Builder
	for _, name := range names {
		var zf *zip.File
		for _, f := range zr.File {
			if f.Name == name {
				zf = f
				break
			}
		}
		if zf == nil {
			continue
		}
		rc, e := zf.Open()
		if e != nil {
			continue
		}
		dec := xml.NewDecoder(rc)
		for {
			tok, e := dec.Token()
			if e != nil {
				break
			}
			if se, ok := tok.(xml.StartElement); ok {
				switch se.Name.Local {
				case "p":
					if b.Len() > 0 {
						b.WriteByte('\n')
					}
				case "br":
					b.WriteByte('\n')
				case "tc":
					if b.Len() > 0 {
						b.WriteByte('\t')
					}
				}
			}
			if ch, ok := tok.(xml.CharData); ok {
				s := strings.TrimSpace(string(ch))
				if s != "" {
					if b.Len() > 0 {
						last := b.String()[b.Len()-1]
						if last != '\n' && last != '\t' {
							b.WriteByte(' ')
						}
					}
					b.WriteString(s)
				}
				if b.Len() >= 256*1024 {
					break
				}
			}
		}
		rc.Close()
		if b.Len() >= 256*1024 {
			break
		}
	}
	kind := "document"
	if strings.HasSuffix(strings.ToLower(path), ".xlsx") {
		kind = "spreadsheet"
	}
	if strings.HasSuffix(strings.ToLower(path), ".pptx") {
		kind = "presentation"
	}
	return kind, b.String(), nil
}

// ---------------------------------------------------------------- 偏好

// PrefStore 是极简偏好存储（原子写 JSON）。
type PrefStore struct {
	mu    sync.Mutex
	path  string
	Prefs map[string]any
}

// NewPrefStore 加载（或初始化）偏好。
func NewPrefStore(dataDir string) *PrefStore {
	recommended := runtime.NumCPU()
	if recommended < 1 {
		recommended = 1
	}
	if recommended > 8 {
		recommended = 8
	}
	p := &PrefStore{path: filepath.Join(dataDir, "prefs.json"), Prefs: map[string]any{
		"maxConcurrent":   recommended,
		"overwrite":       "ask",
		"defaultCodepage": "auto",
		"createSubdir":    true,
		"fixEncoding":     true,
	}}
	if data, err := os.ReadFile(p.path); err == nil {
		_ = json.Unmarshal(data, &p.Prefs)
	}
	return p
}

func (p *PrefStore) get() map[string]any {
	p.mu.Lock()
	defer p.mu.Unlock()
	cp := map[string]any{}
	for k, v := range p.Prefs {
		cp[k] = v
	}
	return cp
}

// MaxConcurrent 返回偏好中的并发上限，供服务启动时初始化任务队列。
func (p *PrefStore) MaxConcurrent() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch n := p.Prefs["maxConcurrent"].(type) {
	case float64:
		if int(n) > 0 {
			return int(n)
		}
	case int:
		if n > 0 {
			return n
		}
	}
	return 1
}

func (p *PrefStore) set(patch map[string]any) error {
	p.mu.Lock()
	for k, v := range patch {
		p.Prefs[k] = v
	}
	data, err := json.MarshalIndent(p.Prefs, "", "  ")
	p.mu.Unlock()
	if err != nil {
		return err
	}
	tmp := p.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p.path)
}

func (s *Server) handlePrefsGet(w http.ResponseWriter, r *http.Request) {
	ok(w, s.Prefs.get())
}

func (s *Server) handlePrefsSet(w http.ResponseWriter, r *http.Request) {
	var patch map[string]any
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		fail(w, apperr.New(apperr.CodeInternal, "请求格式错误", ""))
		return
	}
	if err := s.Prefs.set(patch); err != nil {
		fail(w, apperr.Wrap(err, "保存偏好失败"))
		return
	}
	// 并发上限运行时生效
	if v, okv := patch["maxConcurrent"]; okv {
		switch n := v.(type) {
		case float64:
			s.Jobs.SetLimit(int(n))
		case int:
			s.Jobs.SetLimit(n)
		}
	}
	ok(w, s.Prefs.get())
}

// handleAuthorizeRegister 把前端通过飞牛 SDK 授权得到的目录登记为可访问根。
// 后端会在此刻做真实的文件系统校验（realpath + 打开），因此无法被前端伪造。
type registerReq struct {
	Paths []string `json:"paths"`
}

func (s *Server) handleAuthorizeRegister(w http.ResponseWriter, r *http.Request) {
	var req registerReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fail(w, apperr.New(apperr.CodeInternal, "请求格式错误", ""))
		return
	}
	added := make([]string, 0, len(req.Paths))
	rejected := make([]string, 0, len(req.Paths))
	for _, p := range req.Paths {
		if err := s.Guard.AddRoot(p); err != nil {
			rejected = append(rejected, p)
			continue
		}
		added = append(added, p)
	}
	ok(w, map[string]any{
		"registered":        added,
		"rejected":          rejected,
		"accessibleFolders": s.Guard.Roots(),
	})
}
