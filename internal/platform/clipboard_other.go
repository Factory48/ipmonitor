//go:build !(linux && (amd64 || arm64)) && !(windows && (amd64 || arm64))

package platform

// WatchClipboard is unsupported on this OS or architecture.
func WatchClipboard(onText func(string)) (stop func(), err error) {
	return nil, ErrUnsupported
}
