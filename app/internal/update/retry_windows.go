package update

import (
	"errors"
	"time"

	"golang.org/x/sys/windows"
)

func retryFileAccess(run func() error) error {
	deadline := time.Now().Add(5 * time.Second)
	for {
		err := run()
		if (!errors.Is(err, windows.ERROR_ACCESS_DENIED) && !errors.Is(err, windows.ERROR_SHARING_VIOLATION) && !errors.Is(err, windows.ERROR_LOCK_VIOLATION)) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(50 * time.Millisecond)
	}
}
