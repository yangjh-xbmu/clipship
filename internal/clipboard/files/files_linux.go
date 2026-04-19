//go:build linux && !clipship_fake

package files

func ReadFiles() ([]Entry, error) {
	return nil, ErrUnsupported
}
