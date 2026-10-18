package fetch

import (
	"errors"
	"syscall"
)

// wsaeconnrefused is Winsock's "connection refused" error, which Windows
// returns instead of syscall.ECONNREFUSED. The syscall package doesn't
// define it.
const wsaeconnrefused syscall.Errno = 10061

func isRefused(err error) bool {
	return errors.Is(err, wsaeconnrefused) || errors.Is(err, syscall.ECONNREFUSED)
}
