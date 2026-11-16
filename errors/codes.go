package errors

// 业务错误码约定：HTTP 状态码 * 100 + 序号。
// 错误码一旦发布必须保持稳定，不允许随意调整。
const (
	CodeOK = 0

	CodeBadRequest       = 40000
	CodeValidationFailed = 40001
	CodeMalformedJSON    = 40002
	CodeUnauthorized     = 40100
	CodeTokenExpired     = 40101
	CodeTokenInvalid     = 40102
	CodeSessionRevoked   = 40103
	CodeForbidden        = 40300
	CodePermissionDenied = 40301
	CodeTenantDenied     = 40302
	CodeNotFound         = 40400
	CodeMethodNotAllowed = 40500
	CodeConflict         = 40900
	CodePayloadTooLarge  = 41300
	CodeTooManyRequests  = 42900
	CodeInternal         = 50000
	CodeNotImplemented   = 50100
	CodeUnavailable      = 50300
	CodeTimeout          = 50400
	CodeClientClosed     = 49900
)
