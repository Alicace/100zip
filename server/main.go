// 100zip 常驻服务入口。
//
// 运行方式（生产）：cmd/main 启动本二进制，监听 ${TRIM_APPDEST}/app.sock，由飞牛统一网关转发。
// 运行方式（本地开发）：--addr 127.0.0.1:8737 走 TCP，便于 Windows 调试。
//
// 设计约束：监听地址只允许 Unix Socket 或 127.0.0.1（不对外暴露端口）。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/100zip/100zip/internal/engine"
	"github.com/100zip/100zip/internal/httpapi"
	"github.com/100zip/100zip/internal/jobs"
	"github.com/100zip/100zip/internal/paths"
	"github.com/100zip/100zip/internal/vault"
)

var version = "0.1.0-dev"

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

func main() {
	var (
		socket    = flag.String("socket", "", "Unix socket 路径（生产模式）")
		addr      = flag.String("addr", "", "TCP 监听地址（开发模式，仅允许 127.0.0.1）")
		dataDir   = flag.String("data", "", "数据目录（默认 $TRIM_PKGVAR 或 ./tmp/data）")
		wwwDir    = flag.String("www", "", "前端静态目录（默认 $TRIM_APPDEST/app/www 或 ./app/www）")
		engineBin = flag.String("engine", "", "引擎二进制（默认自动探测）")
		workers   = flag.Int("workers", 0, "任务并发数（0=按偏好/CPU 核心自动设置）")
		showVer   = flag.Bool("version", false, "打印版本后退出")
	)
	var allowRoots multiFlag
	flag.Var(&allowRoots, "allow-root", "允许访问的根目录（可重复；开发模式使用）")
	flag.Parse()

	if *showVer {
		fmt.Println(version)
		return
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	data := firstNonEmpty(*dataDir, os.Getenv("TRIM_PKGVAR"), filepath.Join(".", "tmp", "data"))
	if err := os.MkdirAll(data, 0o700); err != nil {
		slog.Error("create data dir", "err", err)
		os.Exit(1)
	}
	www := firstNonEmpty(*wwwDir, envJoin("TRIM_APPDEST", "app", "www"), filepath.Join(".", "app", "www"))

	bin, err := engine.ResolveEngine(*engineBin, os.Getenv("TRIM_APPDEST"))
	if err != nil {
		slog.Error("engine not found", "err", err)
		os.Exit(1)
	}
	eng, err := engine.NewSevenZip(bin)
	if err != nil {
		slog.Error("init engine", "err", err)
		os.Exit(1)
	}

	roots := append([]string{}, allowRoots...)
	roots = append(roots, splitList(os.Getenv("TRIM_DATA_ACCESSIBLE_PATHS"))...)
	roots = append(roots, splitList(os.Getenv("TRIM_DATA_SHARE_PATHS"))...)
	guard := paths.NewGuard(roots)

	prefs := httpapi.NewPrefStore(data)
	initialWorkers := *workers
	if initialWorkers < 1 {
		initialWorkers = runtime.NumCPU()
		if initialWorkers > 8 {
			initialWorkers = 8
		}
	}
	if configured := prefs.MaxConcurrent(); configured > 0 {
		initialWorkers = configured
	}
	mgr := jobs.NewManager(data, initialWorkers, 200)
	vlt, verr := vault.Open(data)
	if verr != nil {
		slog.Warn("vault unavailable", "err", verr)
	}
	srv := &httpapi.Server{
		Version: version,
		Engine:  eng,
		Jobs:    mgr,
		Guard:   guard,
		DataDir: data,
		WwwDir:  www,
		Prefs:   prefs,
		Vault:   vlt,
	}

	ln, err := listen(*socket, *addr)
	if err != nil {
		slog.Error("listen", "err", err)
		os.Exit(1)
	}

	httpSrv := &http.Server{
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	go func() {
		slog.Info("ready", "version", version, "engine", bin, "roots", guard.Roots(), "listen", ln.Addr().String())
		if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("serve", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutdownCtx)
	if *socket != "" {
		_ = os.Remove(*socket)
	}
}

// listen 优先使用 Unix Socket；否则使用 127.0.0.1 上的 TCP。
func listen(socket, addr string) (net.Listener, error) {
	if socket != "" {
		_ = os.Remove(socket)
		if err := os.MkdirAll(filepath.Dir(socket), 0o755); err != nil {
			return nil, err
		}
		return net.Listen("unix", socket)
	}
	if addr == "" {
		addr = "127.0.0.1:8737"
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	if host != "127.0.0.1" && host != "localhost" && host != "::1" {
		return nil, fmt.Errorf("只允许监听 127.0.0.1，收到: %s", host)
	}
	return net.Listen("tcp", addr)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func envJoin(key string, parts ...string) string {
	base := os.Getenv(key)
	if base == "" {
		return ""
	}
	return filepath.Join(append([]string{base}, parts...)...)
}

func splitList(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	sep := ":"
	if os.PathSeparator == '\\' {
		sep = ";"
	}
	var out []string
	for _, p := range strings.Split(v, sep) {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
