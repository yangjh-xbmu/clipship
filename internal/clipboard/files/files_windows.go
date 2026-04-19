//go:build windows && !clipship_fake

package files

// Stub; real CF_HDROP implementation lands in Task 13.
func ReadFiles() ([]Entry, error) {
	return nil, ErrUnsupported
}
