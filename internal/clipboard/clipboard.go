package clipboard

import (
	"errors"

	cb "golang.design/x/clipboard"
)

var initErr error

func init() {
	initErr = cb.Init()
}

func ReadPNG() ([]byte, error) {
	if initErr != nil {
		return nil, initErr
	}
	b := cb.Read(cb.FmtImage)
	if len(b) == 0 {
		return nil, errors.New("clipboard has no image")
	}
	return b, nil
}

func WriteText(s string) error {
	if initErr != nil {
		return initErr
	}
	cb.Write(cb.FmtText, []byte(s))
	return nil
}
