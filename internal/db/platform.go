package db

import (
	"os"
)

func init() {
	osRemove = osRemoveReal
}

func osRemoveReal(path string) error {
	return os.Remove(path)
}
