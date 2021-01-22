package utils

import (
	"io"
	"strings"
)

func ReadFromBIO(reader io.Reader) (string, error) {
	buf := new(strings.Builder)
	_, err := io.Copy(buf, reader)
	if err != nil {
		return "", err
	}

	return buf.String(), err
}
