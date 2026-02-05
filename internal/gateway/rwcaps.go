package gateway

// StatusCapture is implemented by ResponseWriter wrappers that capture the status code.
type StatusCapture interface {
	StatusCode() int
}
