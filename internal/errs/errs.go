// Package errs 定义全工具统一的三段式错误：稳定错误码 + 用户可读提示 + 技术根因（只进日志）。
// 错误码段位表见 docs/error-codes.md，新增码必须先登记。
package errs

import (
	"errors"
	"fmt"
	"net/http"
)

// 系统段（5xxx）通用错误码。
const (
	CodeInternal      = 5001 // 系统内部错误
	CodeBadRequest    = 5002 // 请求参数不合法
	CodeNotFound      = 5003 // 资源不存在
	CodeUnauthorized  = 5004 // 未授权的请求
	CodeMasterKeyFail = 5005 // 主密钥不可用
)

// E 是对外错误的唯一形态。
type E struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	cause   error  // 技术根因，绝不进 API 响应
}

// Error 实现 error 接口；Error() 只含码与提示，根因通过 Unwrap 暴露给日志层。
func (e *E) Error() string {
	if e.cause == nil {
		return fmt.Sprintf("[%d] %s", e.Code, e.Message)
	}
	return fmt.Sprintf("[%d] %s: %v", e.Code, e.Message, e.cause)
}

// Unwrap 暴露技术根因，供 errors.Is/As 与日志使用。
func (e *E) Unwrap() error { return e.cause }

// From 提取错误中的业务错误；非业务错误包装为 CodeInternal。
func From(err error) *E {
	if err == nil {
		return nil
	}
	var e *E
	if errors.As(err, &e) {
		return e
	}
	return &E{Code: CodeInternal, Message: "系统内部错误", cause: err}
}

// Cause 返回技术根因（可能为 nil），只进日志。
func (e *E) Cause() error { return e.cause }

// New 构造业务错误。
func New(code int, message string) *E { return &E{Code: code, Message: message} }

// Wrap 构造带技术根因的业务错误。
func Wrap(code int, message string, cause error) *E {
	return &E{Code: code, Message: message, cause: cause}
}

// HTTPStatus 将错误码映射为 HTTP 状态码。
func (e *E) HTTPStatus() int {
	switch {
	case e.Code == CodeUnauthorized:
		return http.StatusUnauthorized
	case e.Code == CodeNotFound:
		return http.StatusNotFound
	case e.Code == CodeBadRequest:
		return http.StatusBadRequest
	case e.Code == CodeMasterKeyFail:
		return http.StatusInternalServerError
	case e.Code >= 5000:
		return http.StatusInternalServerError
	default:
		// 业务判定类错误（连通性测试等）以 200 返回，由响应体 code 表达结果
		return http.StatusOK
	}
}
