package model

import "time"

// 同步模式与状态枚举（对外契约）。
const (
	ModeM1 = "m1" // SSH 直推
	ModeM2 = "m2" // 中转 registry

	TaskQueued   = "queued"
	TaskRunning  = "running"
	TaskSuccess  = "success"
	TaskFailed   = "failed"
	TaskCanceled = "canceled"

	StepPending  = "pending"
	StepRunning  = "running"
	StepSuccess  = "success"
	StepFailed   = "failed"
	StepSkipped  = "skipped" // 幂等命中：已存在
	StepCanceled = "canceled"
)

// TaskRecord 同步任务的持久化形态。
type TaskRecord struct {
	ID              string     `json:"id"`
	Mode            string     `json:"mode"`
	Status          string     `json:"status"`
	Code            int        `json:"code"` // 任务级错误码（4005/4006 等）
	Message         string     `json:"message"`
	ImageRefs       []string   `json:"image_refs"`
	TargetServerIDs []string   `json:"target_server_ids"`
	RelayRegistryID string     `json:"relay_registry_id"`
	Steps           []TaskStep `json:"steps"`
	Bytes           int64      `json:"bytes"` // 全任务传输字节总量
	Logs            []LogLine  `json:"logs"`  // 终态时落盘的日志快照（最多 200 行）
	CreatedAt       time.Time  `json:"created_at"`
	StartedAt       time.Time  `json:"started_at"`
	FinishedAt      time.Time  `json:"finished_at"`
}

// TaskStep 镜像 × 目标 的子步骤。
type TaskStep struct {
	ImageRef   string    `json:"image_ref"`
	Target     string    `json:"target"` // 服务器名称或中转 registry 名称
	TargetID   string    `json:"target_id"`
	Status     string    `json:"status"`
	Bytes      int64     `json:"bytes"`
	Skipped    bool      `json:"skipped"`
	ErrorCode  int       `json:"error_code"`
	Message    string    `json:"message"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
}

// TaskInput 创建任务请求体。
type TaskInput struct {
	Mode            string   `json:"mode"` // m1 / m2，缺省 m1
	ImageIDs        []string `json:"image_ids"`
	TargetServerIDs []string `json:"target_server_ids"`
	RelayRegistryID string   `json:"relay_registry_id"`
}

// LogLine 任务日志行（SSE 与轮询共用）。
type LogLine struct {
	Seq   int    `json:"seq"`
	Time  string `json:"time"`
	Level string `json:"level"`
	Line  string `json:"line"`
}
