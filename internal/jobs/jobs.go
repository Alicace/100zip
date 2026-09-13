// Package jobs 提供任务队列、状态机与持久化。
// 契约见 docs/06-接口与数据模型.md；状态：
// queued → running → done|failed|cancelled；
// 解压遇密码类错误 → waiting_password（输入密码后 Requeue 续跑，再次稍后 → manual）。
package jobs

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/100zip/100zip/internal/apperr"
)

// State 是任务状态。
type State string

const (
	StateQueued    State = "queued"
	StateRunning   State = "running"
	StateWaiting   State = "waiting_password"
	StateManual    State = "manual"
	StateDone      State = "done"
	StateFailed    State = "failed"
	StateCancelled State = "cancelled"
)

// ErrorInfo 是任务失败信息（面向用户）。
type ErrorInfo struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint"`
	Detail  string `json:"detail,omitempty"`
}

// Progress 是任务进度快照。
type Progress struct {
	Percent    float64 `json:"percent"`
	Bytes      int64   `json:"bytes"`
	Files      int     `json:"files"`
	SpeedBps   float64 `json:"speedBps,omitempty"`
	ETASeconds float64 `json:"etaSeconds,omitempty"`
}

// Job 是一个解压/压缩任务。
type Job struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	Title      string         `json:"title,omitempty"`
	State      State          `json:"state"`
	CreatedAt  time.Time      `json:"createdAt"`
	StartedAt  *time.Time     `json:"startedAt,omitempty"`
	FinishedAt *time.Time     `json:"finishedAt,omitempty"`
	SrcPath    string         `json:"srcPath,omitempty"`
	DestPath   string         `json:"destPath,omitempty"`
	Params     map[string]any `json:"params,omitempty"`
	// NeedsPassword 标记任务因密码类错误暂停；PasswordPrompts 记录「稍后」次数。
	NeedsPassword   bool       `json:"needsPassword,omitempty"`
	PasswordPrompts int        `json:"passwordPrompts,omitempty"`
	Progress        Progress   `json:"progress"`
	Error           *ErrorInfo `json:"error,omitempty"`
	LogTail         []string   `json:"logTail,omitempty"`
}

// TaskFunc 是任务真正要做的事；通过 ctx 支持取消。
type TaskFunc func(ctx context.Context, j *Job, report func(Progress, string)) error

// Manager 管理任务生命周期。
type Manager struct {
	mu      sync.Mutex
	jobs    map[string]*Job
	order   []string
	queue   chan string
	fns     map[string]TaskFunc
	cancels map[string]context.CancelFunc
	maxKeep int
	dataDir string
	sem     chan struct{} // 并发闸门（运行时可调整）
	limit   int
}

// NewManager 创建任务管理器并启动 worker。
func NewManager(dataDir string, workers, maxKeep int) *Manager {
	if workers < 1 {
		workers = 1
	}
	if maxKeep < 10 {
		maxKeep = 200
	}
	m := &Manager{
		jobs:    map[string]*Job{},
		queue:   make(chan string, 128),
		fns:     map[string]TaskFunc{},
		cancels: map[string]context.CancelFunc{},
		maxKeep: maxKeep,
		dataDir: dataDir,
		sem:     make(chan struct{}, workers),
		limit:   workers,
	}
	m.load()
	// worker 数量固定（取较大值），并发上限由 sem 闸门动态控制
	const pool = 8
	for i := 0; i < pool; i++ {
		go m.worker()
	}
	return m
}

// Limit 返回当前并发上限。
func (m *Manager) Limit() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.limit
}

// SetLimit 运行时调整并发上限（1..32），立即对新任务生效。
func (m *Manager) SetLimit(n int) int {
	if n < 1 {
		n = 1
	}
	if n > 32 {
		n = 32
	}
	m.mu.Lock()
	m.sem = make(chan struct{}, n)
	m.limit = n
	m.mu.Unlock()
	return n
}

// Enqueue 创建并排队一个任务。
func (m *Manager) Enqueue(j *Job, fn TaskFunc) *Job {
	m.mu.Lock()
	j.ID = newID()
	j.State = StateQueued
	j.CreatedAt = time.Now()
	m.jobs[j.ID] = j
	m.order = append(m.order, j.ID)
	m.fns[j.ID] = fn
	m.trimLocked()
	m.mu.Unlock()
	m.persist()
	m.queue <- j.ID
	return j
}

// Get 返回任务副本。
func (m *Manager) Get(id string) (*Job, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok {
		return nil, false
	}
	cp := *j
	return &cp, true
}

// List 按创建时间倒序返回任务副本。
func (m *Manager) List(limit int) []*Job {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 || limit > len(m.order) {
		limit = len(m.order)
	}
	out := make([]*Job, 0, limit)
	for i := len(m.order) - 1; i >= 0 && len(out) < limit; i-- {
		if j, ok := m.jobs[m.order[i]]; ok {
			cp := *j
			out = append(out, &cp)
		}
	}
	return out
}

// Cancel 取消任务：排队中直接标记；运行中触发 ctx 取消。
func (m *Manager) Cancel(id string) bool {
	m.mu.Lock()
	j, ok := m.jobs[id]
	if !ok {
		m.mu.Unlock()
		return false
	}
	switch j.State {
	case StateQueued, StateWaiting, StateManual:
		j.State = StateCancelled
		now := time.Now()
		j.FinishedAt = &now
		j.NeedsPassword = false
		delete(m.fns, id)
		m.mu.Unlock()
		m.persist()
		return true
	case StateRunning:
		if c, ok := m.cancels[id]; ok {
			c()
		}
		m.mu.Unlock()
		return true
	default:
		m.mu.Unlock()
		return false
	}
}

// Delete 删除一条任务记录（运行中的不允许删除）。
func (m *Manager) Delete(id string) bool {
	m.mu.Lock()
	j, ok := m.jobs[id]
	if !ok || j.State == StateRunning || j.State == StateQueued {
		m.mu.Unlock()
		return false
	}
	delete(m.jobs, id)
	for i, v := range m.order {
		if v == id {
			m.order = append(m.order[:i], m.order[i+1:]...)
			break
		}
	}
	m.mu.Unlock()
	m.persist()
	return true
}

// Requeue 让「等待密码/手动」任务带着新密码重新入队；fn 由调用方按原参数重建。
func (m *Manager) Requeue(id string, fn TaskFunc) bool {
	m.mu.Lock()
	j, ok := m.jobs[id]
	if !ok || (j.State != StateWaiting && j.State != StateManual) || fn == nil {
		m.mu.Unlock()
		return false
	}
	j.State = StateQueued
	j.NeedsPassword = false
	j.Error = nil
	j.FinishedAt = nil
	m.fns[id] = fn
	m.mu.Unlock()
	m.persist()
	m.queue <- id
	return true
}

// Defer 把「等待密码」任务转为手动任务（不再自动运行）。
func (m *Manager) Defer(id string) bool {
	m.mu.Lock()
	j, ok := m.jobs[id]
	if !ok || j.State != StateWaiting {
		m.mu.Unlock()
		return false
	}
	j.State = StateManual
	j.PasswordPrompts++
	m.mu.Unlock()
	m.persist()
	return true
}

// ClearFinished 清空所有已结束任务，返回删除条数。
func (m *Manager) ClearFinished() int {
	m.mu.Lock()
	kept := make([]string, 0, len(m.order))
	removed := 0
	for _, id := range m.order {
		j, ok := m.jobs[id]
		if !ok {
			continue
		}
		if j.State == StateRunning || j.State == StateQueued {
			kept = append(kept, id)
			continue
		}
		delete(m.jobs, id)
		removed++
	}
	m.order = kept
	m.mu.Unlock()
	if removed > 0 {
		m.persist()
	}
	return removed
}

func (m *Manager) worker() {
	for id := range m.queue {
		m.mu.Lock()
		j, ok := m.jobs[id]
		fn, hasFn := m.fns[id]
		if !ok || !hasFn || j.State != StateQueued {
			m.mu.Unlock()
			continue
		}
		ctx, cancel := context.WithCancel(context.Background())
		m.cancels[id] = cancel
		now := time.Now()
		j.State = StateRunning
		j.StartedAt = &now
		m.mu.Unlock()
		m.persist()

		// 获取并发闸门（运行时可变）
		m.mu.Lock()
		sem := m.sem
		m.mu.Unlock()
		sem <- struct{}{}

		// 进度包装：补算速度与剩余时间
		started := time.Now()
		lastTime := started
		var lastBytes int64
		err := fn(ctx, j, func(p Progress, line string) {
			nowT := time.Now()
			if d := nowT.Sub(lastTime).Seconds(); d >= 0.8 {
				if p.Bytes > lastBytes {
					p.SpeedBps = float64(p.Bytes-lastBytes) / d
				}
				lastBytes = p.Bytes
				lastTime = nowT
			}
			if p.Percent > 0 && p.Percent < 100 {
				elapsed := nowT.Sub(started).Seconds()
				p.ETASeconds = elapsed * (100 - p.Percent) / p.Percent
			}
			m.mu.Lock()
			if cur, ok := m.jobs[id]; ok {
				cur.Progress = p
				if line != "" {
					cur.LogTail = append(cur.LogTail, line)
					if len(cur.LogTail) > 50 {
						cur.LogTail = cur.LogTail[len(cur.LogTail)-50:]
					}
				}
			}
			m.mu.Unlock()
		})
		<-sem

		m.mu.Lock()
		cancel()
		delete(m.cancels, id)
		fin := time.Now()
		if cur, ok := m.jobs[id]; ok {
			switch {
			case err != nil:
				cur.State = StateFailed
				cur.Error = ToErrorInfo(err)
				cur.FinishedAt = &fin
			case cur.State == StateCancelled:
				// 保持取消状态
				cur.FinishedAt = &fin
			case cur.State == StateWaiting || cur.State == StateManual:
				// 保持等待密码/手动状态，等用户输入密码后由 Requeue 重建续跑
			default:
				cur.State = StateDone
				cur.Progress.Percent = 100
				cur.FinishedAt = &fin
			}
		}
		delete(m.fns, id)
		m.mu.Unlock()
		m.persist()
	}
}

// ---------------------------------------------------------------- 持久化

func (m *Manager) indexFile() string {
	return filepath.Join(m.dataDir, "jobs.json")
}

func (m *Manager) persist() {
	if m.dataDir == "" {
		return
	}
	m.mu.Lock()
	list := make([]*Job, 0, len(m.order))
	for _, id := range m.order {
		if j, ok := m.jobs[id]; ok {
			cp := *j
			list = append(list, &cp)
		}
	}
	m.mu.Unlock()
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return
	}
	tmp := m.indexFile() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, m.indexFile())
}

func (m *Manager) load() {
	if m.dataDir == "" {
		return
	}
	data, err := os.ReadFile(m.indexFile())
	if err != nil {
		return
	}
	var list []*Job
	if err := json.Unmarshal(data, &list); err != nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, j := range list {
		if j.State == StateRunning || j.State == StateQueued {
			j.State = StateFailed
			j.Error = &ErrorInfo{Code: "INTERNAL", Message: "应用重启导致任务中断", Hint: "请重新发起任务"}
		}
		m.jobs[j.ID] = j
		m.order = append(m.order, j.ID)
	}
}

func (m *Manager) trimLocked() {
	for len(m.order) > m.maxKeep {
		oldest := m.order[0]
		if j, ok := m.jobs[oldest]; ok && (j.State == StateRunning || j.State == StateQueued) {
			break
		}
		delete(m.jobs, oldest)
		m.order = m.order[1:]
	}
}

// ToErrorInfo 把任意错误转成面向用户的错误信息。
func ToErrorInfo(err error) *ErrorInfo {
	if err == nil {
		return nil
	}
	if ae, ok := err.(*apperr.Error); ok {
		return &ErrorInfo{Code: string(ae.Code), Message: ae.Message, Hint: ae.Hint, Detail: ae.Detail}
	}
	return &ErrorInfo{Code: "INTERNAL", Message: err.Error(), Hint: "请查看诊断信息"}
}

func newID() string {
	return "j_" + time.Now().Format("20060102150405") + "_" + randHex(6)
}

func randHex(n int) string {
	const hexDigits = "0123456789abcdef"
	b := make([]byte, n)
	seed := time.Now().UnixNano()
	for i := range b {
		seed = seed*6364136223846793005 + 1442695040888963407
		b[i] = hexDigits[(seed>>33)&0xf]
	}
	return string(b)
}
